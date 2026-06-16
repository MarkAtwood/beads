package flatfile

import (
	"context"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// flatFileTx implements storage.Transaction by delegating to the store's methods.
// For flat-file, each file write is already atomic (write-then-rename), so the
// "transaction" is a logical grouping. No rollback of already-written files.
type flatFileTx struct {
	s *FlatFileStore
}

// RunInTransaction executes fn within a logical transaction scope.
func (s *FlatFileStore) RunInTransaction(ctx context.Context, _ string, fn func(tx storage.Transaction) error) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	tx := &flatFileTx{s: s}
	return fn(tx)
}

func (tx *flatFileTx) CreateIssue(ctx context.Context, issue *types.Issue, actor string) error {
	return tx.s.CreateIssue(ctx, issue, actor)
}

func (tx *flatFileTx) CreateIssues(ctx context.Context, issues []*types.Issue, actor string) error {
	return tx.s.CreateIssues(ctx, issues, actor)
}

func (tx *flatFileTx) UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error {
	return tx.s.UpdateIssue(ctx, id, updates, actor)
}

func (tx *flatFileTx) CloseIssue(ctx context.Context, id string, reason string, actor string, session string) error {
	return tx.s.CloseIssue(ctx, id, reason, actor, session)
}

func (tx *flatFileTx) DeleteIssue(ctx context.Context, id string) error {
	return tx.s.DeleteIssue(ctx, id)
}

func (tx *flatFileTx) GetIssue(ctx context.Context, id string) (*types.Issue, error) {
	return tx.s.GetIssue(ctx, id)
}

func (tx *flatFileTx) SearchIssues(ctx context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error) {
	return tx.s.SearchIssues(ctx, query, filter)
}

func (tx *flatFileTx) AddDependency(ctx context.Context, dep *types.Dependency, actor string) error {
	return tx.s.AddDependency(ctx, dep, actor)
}

func (tx *flatFileTx) AddDependencyWithOptions(ctx context.Context, dep *types.Dependency, actor string, _ storage.DependencyAddOptions) error {
	return tx.s.AddDependency(ctx, dep, actor)
}

func (tx *flatFileTx) RemoveDependency(ctx context.Context, issueID, dependsOnID string, actor string) error {
	return tx.s.RemoveDependency(ctx, issueID, dependsOnID, actor)
}

func (tx *flatFileTx) GetDependencyRecords(_ context.Context, issueID string) ([]*types.Dependency, error) {
	issue, err := tx.s.readIssue(issueID)
	if err != nil {
		return nil, err
	}
	return issue.Dependencies, nil
}

func (tx *flatFileTx) CycleThroughEdges(_ context.Context, edges [][2]string) (string, error) {
	if len(edges) == 0 {
		return "", nil
	}

	// Build the full dependency graph from all issues on disk.
	all, err := tx.s.loadAllIssues()
	if err != nil {
		return "", err
	}
	adj := make(map[string][]string)
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type != types.DepParentChild {
				adj[issue.ID] = append(adj[issue.ID], dep.DependsOnID)
			}
		}
	}

	// DFS cycle detection: for each new edge's source, walk the graph
	// looking for a cycle that includes that edge.
	for _, e := range edges {
		target := e[1]
		start := e[0] // issue that depends on target
		// A cycle exists if we can reach start from target via the graph.
		visited := make(map[string]bool)
		var path []string
		if findPath(adj, target, start, visited, &path) {
			cycle := target
			for _, id := range path {
				cycle += " -> " + id
			}
			cycle += " -> " + target
			return cycle, nil
		}
	}
	return "", nil
}

// findPath does DFS from 'from' looking for 'to'. Returns true if reachable,
// with path containing the intermediate nodes.
func findPath(adj map[string][]string, from, to string, visited map[string]bool, path *[]string) bool {
	if from == to {
		*path = append(*path, to)
		return true
	}
	if visited[from] {
		return false
	}
	visited[from] = true
	*path = append(*path, from)
	for _, next := range adj[from] {
		if findPath(adj, next, to, visited, path) {
			return true
		}
	}
	*path = (*path)[:len(*path)-1]
	return false
}

func (tx *flatFileTx) AddLabel(ctx context.Context, issueID, label, actor string) error {
	return tx.s.AddLabel(ctx, issueID, label, actor)
}

func (tx *flatFileTx) RemoveLabel(ctx context.Context, issueID, label, actor string) error {
	return tx.s.RemoveLabel(ctx, issueID, label, actor)
}

func (tx *flatFileTx) GetLabels(ctx context.Context, issueID string) ([]string, error) {
	return tx.s.GetLabels(ctx, issueID)
}

func (tx *flatFileTx) SetConfig(ctx context.Context, key, value string) error {
	return tx.s.SetConfig(ctx, key, value)
}

func (tx *flatFileTx) GetConfig(ctx context.Context, key string) (string, error) {
	return tx.s.GetConfig(ctx, key)
}

func (tx *flatFileTx) SetMetadata(_ context.Context, key, value string) error {
	return tx.s.setKV(tx.s.configKVPath, "metadata:"+key, value)
}

func (tx *flatFileTx) GetMetadata(_ context.Context, key string) (string, error) {
	return tx.s.getKV(tx.s.configKVPath, "metadata:"+key)
}

func (tx *flatFileTx) SetLocalMetadata(ctx context.Context, key, value string) error {
	return tx.s.SetLocalMetadata(ctx, key, value)
}

func (tx *flatFileTx) GetLocalMetadata(ctx context.Context, key string) (string, error) {
	return tx.s.GetLocalMetadata(ctx, key)
}

func (tx *flatFileTx) AddComment(ctx context.Context, issueID, actor, comment string) error {
	_, err := tx.s.AddIssueComment(ctx, issueID, actor, comment)
	return err
}

func (tx *flatFileTx) ImportIssueComment(ctx context.Context, issueID, author, text string, createdAt time.Time) (*types.Comment, error) {
	return tx.s.ImportIssueComment(ctx, issueID, author, text, createdAt)
}

func (tx *flatFileTx) GetIssueComments(ctx context.Context, issueID string) ([]*types.Comment, error) {
	return tx.s.GetIssueComments(ctx, issueID)
}
