package flatfile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// isBlockingDepType reports whether a dependency type participates in
// blocked-state and cycle detection (mirrors issueops' 'blocks' /
// 'conditional-blocks' filters).
func isBlockingDepType(t types.DependencyType) bool {
	return t == types.DepBlocks || t == types.DepConditionalBlocks
}

// isExternalDepTarget reports whether a dependency target references another
// rig ("external:..."), which skips existence validation.
func isExternalDepTarget(id string) bool {
	return strings.HasPrefix(id, "external:")
}

// blockingAdjacency builds the blocks/conditional-blocks adjacency map used
// for cycle detection.
func blockingAdjacency(all []*types.Issue) map[string][]string {
	adj := make(map[string][]string)
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if isBlockingDepType(dep.Type) {
				adj[issue.ID] = append(adj[issue.ID], dep.DependsOnID)
			}
		}
	}
	return adj
}

// reachable reports whether `to` is reachable from `from` in adj (BFS over
// unique nodes, so diamonds and cycles terminate).
func reachable(adj map[string][]string, from, to string) bool {
	if from == to {
		return true
	}
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, next := range adj[node] {
			if next == to {
				return true
			}
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

// checkDependencyCycle mirrors issueops.CheckDependencyCycleInTx: reject
// self-dependencies for every type, and blocking-edge cycles by a
// reachability probe from the target back to the source.
func (s *FlatFileStore) checkDependencyCycle(dep *types.Dependency, all []*types.Issue) error {
	if dep.IssueID == dep.DependsOnID {
		return fmt.Errorf("cannot add self-dependency: %s cannot depend on itself", dep.IssueID)
	}
	if !isBlockingDepType(dep.Type) {
		return nil
	}
	if reachable(blockingAdjacency(all), dep.DependsOnID, dep.IssueID) {
		return fmt.Errorf("adding dependency would create a cycle")
	}
	return nil
}

// activeStatus mirrors the SQL blocked-state predicates: closed and pinned
// issues neither block nor count as blocked.
func activeStatus(st types.Status) bool {
	return st != types.StatusClosed && st != types.StatusPinned
}

// waitsForGateBlocks mirrors issueops' waitsForGateBlockedSQL predicate: a
// waits-for waiter is blocked while the spawner target has any active
// (non-closed, non-pinned) parent-child child, unless the edge carries an
// any-children gate that is already satisfied by a closed (specifically
// closed, not pinned) child. The target itself need not exist or be active —
// only its children matter, exactly as in the SQL (which never joins the
// target row).
func waitsForGateBlocks(metadata string, hasActiveChild, hasClosedChild bool) bool {
	if !hasActiveChild {
		return false
	}
	if types.ParseWaitsForGateMetadata(metadata) == types.WaitsForAnyChildren && hasClosedChild {
		return false
	}
	return true
}

// computeBlockedSet mirrors the is_blocked projection maintained by
// issueops.RecomputeIsBlockedInTx: an active issue is blocked when it has a
// blocks/conditional-blocks edge to an existing active target, a waits-for
// edge whose gate is unsatisfied (see waitsForGateBlocks), or (fixpoint) a
// parent-child edge to a blocked parent.
func computeBlockedSet(all []*types.Issue) map[string]bool {
	statusOf := make(map[string]types.Status, len(all))
	for _, issue := range all {
		statusOf[issue.ID] = issue.Status
	}
	// Per-spawner child status aggregates for waits-for gates.
	hasActiveChild := make(map[string]bool)
	hasClosedChild := make(map[string]bool)
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type != types.DepParentChild {
				continue
			}
			if activeStatus(issue.Status) {
				hasActiveChild[dep.DependsOnID] = true
			}
			if issue.Status == types.StatusClosed {
				hasClosedChild[dep.DependsOnID] = true
			}
		}
	}
	blocked := make(map[string]bool)
	for _, issue := range all {
		if !activeStatus(issue.Status) {
			continue
		}
		for _, dep := range issue.Dependencies {
			if isBlockingDepType(dep.Type) {
				if st, ok := statusOf[dep.DependsOnID]; ok && activeStatus(st) {
					blocked[issue.ID] = true
					break
				}
			} else if dep.Type == types.DepWaitsFor {
				if waitsForGateBlocks(dep.Metadata, hasActiveChild[dep.DependsOnID], hasClosedChild[dep.DependsOnID]) {
					blocked[issue.ID] = true
					break
				}
			}
		}
	}
	// Inherited parent blocking, iterated to a fixpoint.
	for changed := true; changed; {
		changed = false
		for _, issue := range all {
			if blocked[issue.ID] || !activeStatus(issue.Status) {
				continue
			}
			for _, dep := range issue.Dependencies {
				if dep.Type == types.DepParentChild && blocked[dep.DependsOnID] {
					blocked[issue.ID] = true
					changed = true
					break
				}
			}
		}
	}
	return blocked
}

// AddDependency validates and adds a dependency record to the depending
// issue's JSON. Mirrors issueops.AddDependencyInTx: source/target existence,
// cross-type blocking (GH#1495), self-dependency and cycle rejection,
// idempotent same-type metadata updates, and type-conflict errors. External
// ("external:...") and cross-prefix targets (another rig's beads — the SQL
// wrappers route these as DepTargetExternal via isCrossPrefixDep) skip
// existence validation and the GH#1495 check. The source issue's updated_at
// is deliberately NOT bumped (SQL never touches it here).
func (s *FlatFileStore) AddDependency(_ context.Context, dep *types.Dependency, actor string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	source, err := s.readIssue(dep.IssueID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("issue %s not found", dep.IssueID)
		}
		return err
	}

	isCrossPrefix := types.ExtractPrefix(dep.IssueID) != types.ExtractPrefix(dep.DependsOnID)

	var target *types.Issue
	if !isCrossPrefix && !isExternalDepTarget(dep.DependsOnID) {
		target, err = s.readIssue(dep.DependsOnID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return fmt.Errorf("issue %s not found", dep.DependsOnID)
			}
			return err
		}
	}

	// Cross-type blocking validation (GH#1495): tasks can only block tasks,
	// epics can only block epics. Scoped to DepBlocks only.
	if dep.Type == types.DepBlocks && target != nil {
		sourceIsEpic := source.IssueType == types.TypeEpic
		targetIsEpic := target.IssueType == types.TypeEpic
		if sourceIsEpic != targetIsEpic {
			if sourceIsEpic {
				return fmt.Errorf("epics can only block other epics, not tasks")
			}
			return fmt.Errorf("tasks can only block other tasks, not epics")
		}
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return err
	}
	if err := s.checkDependencyCycle(dep, all); err != nil {
		return err
	}

	metadata := dep.Metadata
	if metadata == "" {
		metadata = "{}"
	}

	// Existing edge between the same pair (any type): same type is an
	// idempotent metadata update, a different type is a conflict.
	for _, existing := range source.Dependencies {
		if existing.DependsOnID != dep.DependsOnID {
			continue
		}
		if existing.Type == dep.Type {
			existing.Metadata = metadata
			return s.writeIssue(source)
		}
		return fmt.Errorf("dependency %s -> %s already exists with type %q (requested %q); remove it first with 'bd dep remove' then re-add",
			dep.IssueID, dep.DependsOnID, existing.Type, dep.Type)
	}

	dep.Metadata = metadata
	if dep.CreatedAt.IsZero() {
		dep.CreatedAt = time.Now().UTC()
	}
	if dep.CreatedBy == "" {
		dep.CreatedBy = actor
	}
	// The SQL reference preserves the source issue's updated_at when adding
	// a dependency (issueops explicitly writes `updated_at = updated_at`),
	// so no bump here.
	source.Dependencies = append(source.Dependencies, dep)
	return s.writeIssue(source)
}

// RemoveDependency removes a dependency from the depending issue's JSON.
// Mirrors issueops.RemoveDependencyInTx: removing a non-existent edge is a
// silent no-op, and edges of every type (including parent-child) match.
func (s *FlatFileStore) RemoveDependency(_ context.Context, issueID, dependsOnID string, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	issue, err := s.readIssue(issueID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			// issueops.RemoveDependencyInTx never validates source existence:
			// a missing source simply has no matching edge row, so removal is
			// part of the same silent no-op contract.
			return nil
		}
		return err
	}

	filtered := make([]*types.Dependency, 0, len(issue.Dependencies))
	found := false
	for _, dep := range issue.Dependencies {
		if dep.DependsOnID == dependsOnID {
			found = true
			continue
		}
		filtered = append(filtered, dep)
	}
	if !found {
		return nil
	}

	issue.Dependencies = filtered
	return s.writeIssue(issue)
}

// GetDependencies returns the issues that issueID depends on. Mirrors
// issueops.GetDependenciesInTx: edges of every type are followed, and targets
// that do not hydrate to an issue (missing, external) are dropped. Any other
// read failure (I/O error, corrupt JSON) fails the query, as it would in SQL.
func (s *FlatFileStore) GetDependencies(_ context.Context, issueID string) ([]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return nil, err
	}

	var result []*types.Issue
	for _, dep := range issue.Dependencies {
		if isExternalDepTarget(dep.DependsOnID) {
			continue
		}
		target, err := s.readIssue(dep.DependsOnID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue // skip missing (e.g. cross-prefix) targets
			}
			return nil, err
		}
		result = append(result, target)
	}
	return result, nil
}

// GetDependents returns issues that depend on issueID (reverse lookup, all
// edge types — mirrors issueops.GetDependentsInTx).
func (s *FlatFileStore) GetDependents(_ context.Context, issueID string) ([]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	var result []*types.Issue
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.DependsOnID == issueID {
				result = append(result, issue)
				break
			}
		}
	}
	return result, nil
}

// GetDependenciesWithMetadata returns dependencies with relationship type info.
func (s *FlatFileStore) GetDependenciesWithMetadata(_ context.Context, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return nil, err
	}

	var result []*types.IssueWithDependencyMetadata
	for _, dep := range issue.Dependencies {
		if isExternalDepTarget(dep.DependsOnID) {
			continue
		}
		target, err := s.readIssue(dep.DependsOnID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue // skip missing (e.g. cross-prefix) targets
			}
			return nil, err
		}
		result = append(result, &types.IssueWithDependencyMetadata{
			Issue:          *target,
			DependencyType: dep.Type,
		})
	}
	return result, nil
}

// GetDependentsWithMetadata returns dependents with relationship type info.
func (s *FlatFileStore) GetDependentsWithMetadata(_ context.Context, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	var result []*types.IssueWithDependencyMetadata
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.DependsOnID == issueID {
				result = append(result, &types.IssueWithDependencyMetadata{
					Issue:          *issue,
					DependencyType: dep.Type,
				})
				break
			}
		}
	}
	return result, nil
}

// GetDependencyTree builds a flattened dependency tree starting from issueID.
// Mirrors issueops.GetDependencyTreeInTx: DFS pre-order, relates-to edges
// excluded, nodes annotated with Depth/ParentID/EdgeFromParent, and traversal
// stops when depth reaches maxDepth (maxDepth=1 yields just the root).
func (s *FlatFileStore) GetDependencyTree(_ context.Context, issueID string, maxDepth int, _ bool, reverse bool) ([]*types.TreeNode, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	issueMap := make(map[string]*types.Issue, len(all))
	for _, issue := range all {
		issueMap[issue.ID] = issue
	}

	// A missing root is an error (issueops.buildDependencyTreeInTx hits
	// GetIssueInTx's wrapped ErrNotFound) — but only when the walk would
	// actually visit it: maxDepth <= 0 returns empty before the lookup.
	// Missing non-root nodes stay silently skipped; SQL never recurses into
	// them because its hydration join drops them first.
	if maxDepth > 0 {
		if _, ok := issueMap[issueID]; !ok {
			return nil, fmt.Errorf("%w: issue %s", storage.ErrNotFound, issueID)
		}
	}

	type edge struct {
		target  string
		depType types.DependencyType
	}
	adj := make(map[string][]edge)
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type == types.DepRelatesTo {
				continue
			}
			if reverse {
				adj[dep.DependsOnID] = append(adj[dep.DependsOnID], edge{issue.ID, dep.Type})
			} else {
				adj[issue.ID] = append(adj[issue.ID], edge{dep.DependsOnID, dep.Type})
			}
		}
	}

	var result []*types.TreeNode
	visited := make(map[string]bool)

	var walk func(id string, depth int, parentID string, edgeFromParent types.DependencyType)
	walk = func(id string, depth int, parentID string, edgeFromParent types.DependencyType) {
		if depth >= maxDepth || visited[id] {
			return
		}
		visited[id] = true

		issue := issueMap[id]
		if issue == nil {
			return
		}

		result = append(result, &types.TreeNode{
			Issue:          *issue,
			Depth:          depth,
			ParentID:       parentID,
			EdgeFromParent: edgeFromParent,
		})

		for _, e := range adj[id] {
			walk(e.target, depth+1, id, e.depType)
		}
	}

	walk(issueID, 0, "", "")
	return result, nil
}

// CountDependencies returns the number of outbound dependency edges of every
// type (mirrors the bare per-source COUNT in the SQL backends).
func (s *FlatFileStore) CountDependencies(_ context.Context, issueID string) (int64, error) {
	if err := s.checkClosed(); err != nil {
		return 0, err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return 0, err
	}
	return int64(len(issue.Dependencies)), nil
}

// CountDependents returns the number of issues that depend on issueID via an
// edge of any type (mirrors the bare per-target COUNT in the SQL backends).
func (s *FlatFileStore) CountDependents(_ context.Context, issueID string) (int64, error) {
	if err := s.checkClosed(); err != nil {
		return 0, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return 0, err
	}

	var count int64
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.DependsOnID == issueID {
				count++
				break
			}
		}
	}
	return count, nil
}
