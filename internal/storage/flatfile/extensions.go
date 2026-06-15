// extensions.go implements the DoltStorage extension interfaces needed for
// HookFiringStore compatibility: MergeSlot, Slots, BulkIssueStore,
// DependencyQueryStore, AnnotationStore, ConfigMetadataStore,
// AdvancedQueryStore, CompactionStore, and misc Storage methods.
package flatfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// ── MergeSlot ─────────────────────────────────────────────────────────

type mergeSlotState struct {
	SlotID  string   `json:"slot_id"`
	Holder  string   `json:"holder"`
	Waiters []string `json:"waiters"`
}

func (s *FlatFileStore) MergeSlotCreate(ctx context.Context, actor string) (*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	state := &mergeSlotState{SlotID: "merge-slot"}
	data, _ := json.MarshalIndent(state, "", "  ")
	data = append(data, '\n')
	if err := os.WriteFile(s.mergeslotPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("flatfile: write merge slot: %w", err)
	}
	return &types.Issue{ID: "merge-slot", Title: "Merge Slot", IssueType: "slot"}, nil
}

func (s *FlatFileStore) MergeSlotCheck(_ context.Context) (*storage.MergeSlotStatus, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	state, err := s.readMergeSlot()
	if err != nil {
		return nil, err
	}
	return &storage.MergeSlotStatus{
		SlotID:    state.SlotID,
		Available: state.Holder == "",
		Holder:    state.Holder,
		Waiters:   state.Waiters,
	}, nil
}

func (s *FlatFileStore) MergeSlotAcquire(_ context.Context, holder, _ string, _ bool) (*storage.MergeSlotResult, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	state, err := s.readMergeSlot()
	if err != nil {
		return nil, err
	}
	if state.Holder == "" {
		state.Holder = holder
		if err := s.writeMergeSlot(state); err != nil {
			return nil, err
		}
		return &storage.MergeSlotResult{SlotID: state.SlotID, Acquired: true, Holder: holder}, nil
	}
	if state.Holder == holder {
		return &storage.MergeSlotResult{SlotID: state.SlotID, Acquired: true, Holder: holder}, nil
	}
	return &storage.MergeSlotResult{SlotID: state.SlotID, Acquired: false, Holder: state.Holder}, nil
}

func (s *FlatFileStore) MergeSlotRelease(_ context.Context, holder, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	state, err := s.readMergeSlot()
	if err != nil {
		return err
	}
	if state.Holder == holder {
		state.Holder = ""
	}
	return s.writeMergeSlot(state)
}

func (s *FlatFileStore) readMergeSlot() (*mergeSlotState, error) {
	data, err := os.ReadFile(s.mergeslotPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &mergeSlotState{SlotID: "merge-slot"}, nil
		}
		return nil, err
	}
	var state mergeSlotState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("flatfile: parse merge slot: %w", err)
	}
	return &state, nil
}

func (s *FlatFileStore) writeMergeSlot(state *mergeSlotState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("flatfile: marshal merge slot: %w", err)
	}
	data = append(data, '\n')
	tmp := s.mergeslotPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("flatfile: write merge slot: %w", err)
	}
	if err := os.Rename(tmp, s.mergeslotPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("flatfile: rename merge slot: %w", err)
	}
	return nil
}

// ── Metadata Slots ────────────────────────────────────────────────────

func (s *FlatFileStore) SlotSet(_ context.Context, issueID, key, value, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	issue, err := s.readIssue(issueID)
	if err != nil {
		return err
	}
	meta := make(map[string]interface{})
	if len(issue.Metadata) > 0 {
		if err := json.Unmarshal(issue.Metadata, &meta); err != nil {
			return fmt.Errorf("flatfile: parse issue metadata: %w", err)
		}
	}
	meta[key] = value
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("flatfile: marshal issue metadata: %w", err)
	}
	issue.Metadata = raw
	issue.UpdatedAt = time.Now().UTC()
	return s.writeIssue(issue)
}

func (s *FlatFileStore) SlotGet(_ context.Context, issueID, key string) (string, error) {
	if err := s.checkClosed(); err != nil {
		return "", err
	}
	issue, err := s.readIssue(issueID)
	if err != nil {
		return "", err
	}
	if len(issue.Metadata) == 0 {
		return "", nil
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(issue.Metadata, &meta); err != nil {
		return "", nil
	}
	if v, ok := meta[key]; ok {
		return fmt.Sprintf("%v", v), nil
	}
	return "", nil
}

func (s *FlatFileStore) SlotClear(_ context.Context, issueID, key, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	issue, err := s.readIssue(issueID)
	if err != nil {
		return err
	}
	if len(issue.Metadata) == 0 {
		return nil
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(issue.Metadata, &meta); err != nil {
		return nil
	}
	delete(meta, key)
	issue.Metadata, _ = json.Marshal(meta)
	issue.UpdatedAt = time.Now().UTC()
	return s.writeIssue(issue)
}

// ── BulkIssueStore ───────────────────────────────────────────────────

func (s *FlatFileStore) CreateIssuesWithFullOptions(ctx context.Context, issues []*types.Issue, actor string, _ storage.BatchCreateOptions) error {
	return s.CreateIssues(ctx, issues, actor)
}

func (s *FlatFileStore) DeleteIssues(ctx context.Context, ids []string, _ bool, _ bool, dryRun bool) (*types.DeleteIssuesResult, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	result := &types.DeleteIssuesResult{}
	for _, id := range ids {
		if dryRun {
			result.DeletedCount++
			continue
		}
		if err := s.DeleteIssue(ctx, id); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			return result, err
		}
		result.DeletedCount++
	}
	return result, nil
}

func (s *FlatFileStore) DeleteIssuesBySourceRepo(_ context.Context, sourceRepo string) (int, error) {
	if err := s.checkClosed(); err != nil {
		return 0, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, issue := range all {
		if issue.SourceRepo == sourceRepo {
			_ = os.Remove(s.issueFilename(issue.ID))
			deleted++
		}
	}
	return deleted, nil
}

func (s *FlatFileStore) UpdateIssueID(_ context.Context, oldID, newID string, issue *types.Issue, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	issue.ID = newID
	issue.UpdatedAt = time.Now().UTC()
	if err := s.writeIssue(issue); err != nil {
		return err
	}
	// Remove old file only after new file is safely written.
	_ = os.Remove(s.issueFilename(oldID))
	return nil
}

func (s *FlatFileStore) ClaimIssue(ctx context.Context, id string, actor string) error {
	return s.UpdateIssue(ctx, id, map[string]interface{}{"assignee": actor, "status": string(types.StatusInProgress)}, actor)
}

func (s *FlatFileStore) ClaimReadyIssue(ctx context.Context, filter types.WorkFilter, actor string) (*types.Issue, error) {
	ready, err := s.GetReadyWork(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(ready) == 0 {
		return nil, storage.ErrNotFound
	}
	issue := ready[0]
	if err := s.ClaimIssue(ctx, issue.ID, actor); err != nil {
		return nil, err
	}
	issue.Assignee = actor
	issue.Status = types.StatusInProgress
	return issue, nil
}

func (s *FlatFileStore) PromoteFromEphemeral(_ context.Context, id string, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	issue, err := s.readIssue(id)
	if err != nil {
		return err
	}
	issue.Ephemeral = false
	issue.UpdatedAt = time.Now().UTC()
	return s.writeIssue(issue)
}

func (s *FlatFileStore) GetNextChildID(_ context.Context, parentID string) (string, error) {
	if err := s.checkClosed(); err != nil {
		return "", err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return "", err
	}
	maxChild := 0
	prefix := parentID + "."
	for _, issue := range all {
		if strings.HasPrefix(issue.ID, prefix) {
			suffix := strings.TrimPrefix(issue.ID, prefix)
			var n int
			if _, err := fmt.Sscanf(suffix, "%d", &n); err == nil && n > maxChild {
				maxChild = n
			}
		}
	}
	return fmt.Sprintf("%s.%d", parentID, maxChild+1), nil
}

// ── DependencyQueryStore ─────────────────────────────────────────────

func (s *FlatFileStore) GetDependencyRecords(_ context.Context, issueID string) ([]*types.Dependency, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	issue, err := s.readIssue(issueID)
	if err != nil {
		return nil, err
	}
	return issue.Dependencies, nil
}

func (s *FlatFileStore) GetDependencyRecordsForIssues(_ context.Context, issueIDs []string) (map[string][]*types.Dependency, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	result := make(map[string][]*types.Dependency, len(issueIDs))
	for _, id := range issueIDs {
		issue, err := s.readIssue(id)
		if err != nil {
			continue
		}
		result[id] = issue.Dependencies
	}
	return result, nil
}

func (s *FlatFileStore) GetAllDependencyRecords(_ context.Context) (map[string][]*types.Dependency, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}
	result := make(map[string][]*types.Dependency, len(all))
	for _, issue := range all {
		if len(issue.Dependencies) > 0 {
			result[issue.ID] = issue.Dependencies
		}
	}
	return result, nil
}

func (s *FlatFileStore) GetDependencyCounts(_ context.Context, issueIDs []string) (map[string]*types.DependencyCounts, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}
	depCounts := make(map[string]int)
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type != "parent-child" {
				depCounts[dep.DependsOnID]++
			}
		}
	}
	result := make(map[string]*types.DependencyCounts, len(issueIDs))
	for _, id := range issueIDs {
		issue, err := s.readIssue(id)
		if err != nil {
			continue
		}
		dc := 0
		for _, dep := range issue.Dependencies {
			if dep.Type != "parent-child" {
				dc++
			}
		}
		result[id] = &types.DependencyCounts{
			DependencyCount: dc,
			DependentCount:  depCounts[id],
		}
	}
	return result, nil
}

func (s *FlatFileStore) GetBlockingInfoForIssues(_ context.Context, issueIDs []string) (map[string][]string, map[string][]string, map[string]string, error) {
	if err := s.checkClosed(); err != nil {
		return nil, nil, nil, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return nil, nil, nil, err
	}
	blockedBy := make(map[string][]string)
	blocks := make(map[string][]string)
	parentMap := make(map[string]string)
	idSet := makeStringSet(issueIDs)

	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type == "parent-child" {
				if idSet[issue.ID] {
					parentMap[issue.ID] = dep.DependsOnID
				}
				continue
			}
			if idSet[issue.ID] {
				blockedBy[issue.ID] = append(blockedBy[issue.ID], dep.DependsOnID)
			}
			if idSet[dep.DependsOnID] {
				blocks[dep.DependsOnID] = append(blocks[dep.DependsOnID], issue.ID)
			}
		}
	}
	return blockedBy, blocks, parentMap, nil
}

func (s *FlatFileStore) IsBlocked(_ context.Context, issueID string) (bool, []string, error) {
	if err := s.checkClosed(); err != nil {
		return false, nil, err
	}
	issue, err := s.readIssue(issueID)
	if err != nil {
		return false, nil, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return false, nil, err
	}
	statusMap := make(map[string]types.Status, len(all))
	for _, a := range all {
		statusMap[a.ID] = a.Status
	}
	var blockers []string
	for _, dep := range issue.Dependencies {
		if dep.Type == "parent-child" {
			continue
		}
		if st, ok := statusMap[dep.DependsOnID]; ok && st != types.StatusClosed {
			blockers = append(blockers, dep.DependsOnID)
		}
	}
	return len(blockers) > 0, blockers, nil
}

func (s *FlatFileStore) GetNewlyUnblockedByClose(_ context.Context, closedIssueID string) ([]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}
	statusMap := make(map[string]types.Status, len(all))
	for _, a := range all {
		statusMap[a.ID] = a.Status
	}
	statusMap[closedIssueID] = types.StatusClosed

	var result []*types.Issue
	for _, issue := range all {
		if !isWorkableStatus(issue.Status) {
			continue
		}
		dependsOnClosed := false
		allResolved := true
		for _, dep := range issue.Dependencies {
			if dep.Type == "parent-child" {
				continue
			}
			if dep.DependsOnID == closedIssueID {
				dependsOnClosed = true
			}
			if st, ok := statusMap[dep.DependsOnID]; ok && st != types.StatusClosed {
				allResolved = false
			}
		}
		if dependsOnClosed && allResolved {
			result = append(result, issue)
		}
	}
	return result, nil
}

func (s *FlatFileStore) DetectCycles(_ context.Context) ([][]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}
	issueMap := make(map[string]*types.Issue, len(all))
	adj := make(map[string][]string)
	for _, issue := range all {
		issueMap[issue.ID] = issue
		for _, dep := range issue.Dependencies {
			if dep.Type != "parent-child" {
				adj[issue.ID] = append(adj[issue.ID], dep.DependsOnID)
			}
		}
	}
	// Simple cycle detection via DFS with coloring.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int)
	var cycles [][]*types.Issue
	var path []string

	var dfs func(node string) bool
	dfs = func(node string) bool {
		color[node] = gray
		path = append(path, node)
		for _, next := range adj[node] {
			if color[next] == gray {
				// Found a cycle — extract it.
				start := -1
				for i, p := range path {
					if p == next {
						start = i
						break
					}
				}
				if start >= 0 {
					var cycle []*types.Issue
					for _, id := range path[start:] {
						if iss, ok := issueMap[id]; ok {
							cycle = append(cycle, iss)
						}
					}
					cycles = append(cycles, cycle)
				}
				return true
			}
			if color[next] == white {
				dfs(next)
			}
		}
		path = path[:len(path)-1]
		color[node] = black
		return false
	}
	for _, issue := range all {
		if color[issue.ID] == white {
			dfs(issue.ID)
		}
	}
	return cycles, nil
}

func (s *FlatFileStore) FindWispDependentsRecursive(_ context.Context, ids []string) (map[string]bool, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}
	// Build reverse dep map.
	dependents := make(map[string][]string)
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type != "parent-child" {
				dependents[dep.DependsOnID] = append(dependents[dep.DependsOnID], issue.ID)
			}
		}
	}
	result := make(map[string]bool)
	queue := make([]string, len(ids))
	copy(queue, ids)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if result[id] {
			continue
		}
		result[id] = true
		for _, dep := range dependents[id] {
			if !result[dep] {
				queue = append(queue, dep)
			}
		}
	}
	return result, nil
}

func (s *FlatFileStore) IterAllDependencyRecords(_ context.Context) (storage.Iter[types.Dependency], error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}
	var deps []*types.Dependency
	for _, issue := range all {
		deps = append(deps, issue.Dependencies...)
	}
	return storage.NewSliceIter(deps), nil
}

func (s *FlatFileStore) CountDependentsByStatus(_ context.Context, issueID string, status types.Status) (int64, error) {
	if err := s.checkClosed(); err != nil {
		return 0, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return 0, err
	}
	var count int64
	for _, issue := range all {
		if issue.Status != status {
			continue
		}
		for _, dep := range issue.Dependencies {
			if dep.DependsOnID == issueID && dep.Type != "parent-child" {
				count++
				break
			}
		}
	}
	return count, nil
}

// ── AnnotationStore ──────────────────────────────────────────────────

func (s *FlatFileStore) AddComment(ctx context.Context, issueID, actor, comment string) error {
	_, err := s.AddIssueComment(ctx, issueID, actor, comment)
	return err
}

func (s *FlatFileStore) ImportIssueComment(_ context.Context, issueID, author, text string, createdAt time.Time) (*types.Comment, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	comment := &types.Comment{
		ID:        fmt.Sprintf("%d", createdAt.UnixNano()),
		IssueID:   issueID,
		Author:    author,
		Text:      text,
		CreatedAt: createdAt,
	}
	dir := filepath.Join(s.commentsDir, issueID)
	_ = os.MkdirAll(dir, 0o755)
	filename := createdAt.Format("2006-01-02T15-04-05.000000000Z") + ".json"
	data, err := json.MarshalIndent(comment, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("flatfile: marshal comment: %w", err)
	}
	data = append(data, '\n')
	path := filepath.Join(dir, filename)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, fmt.Errorf("flatfile: write comment: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("flatfile: rename comment: %w", err)
	}
	return comment, nil
}

func (s *FlatFileStore) GetCommentCounts(_ context.Context, issueIDs []string) (map[string]int, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	result := make(map[string]int, len(issueIDs))
	for _, id := range issueIDs {
		count, _ := s.countCommentFiles(id)
		result[id] = count
	}
	return result, nil
}

func (s *FlatFileStore) GetCommentsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Comment, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	result := make(map[string][]*types.Comment, len(issueIDs))
	for _, id := range issueIDs {
		comments, err := s.GetIssueComments(ctx, id)
		if err != nil {
			return nil, err
		}
		result[id] = comments
	}
	return result, nil
}

func (s *FlatFileStore) GetLabelsForIssues(_ context.Context, issueIDs []string) (map[string][]string, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	result := make(map[string][]string, len(issueIDs))
	for _, id := range issueIDs {
		issue, err := s.readIssue(id)
		if err != nil {
			continue
		}
		result[id] = issue.Labels
	}
	return result, nil
}

// ── ConfigMetadataStore ──────────────────────────────────────────────

func (s *FlatFileStore) GetMetadata(_ context.Context, key string) (string, error) {
	if err := s.checkClosed(); err != nil {
		return "", err
	}
	return s.getKV(s.configKVPath, "metadata:"+key)
}

func (s *FlatFileStore) SetMetadata(_ context.Context, key, value string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	return s.setKV(s.configKVPath, "metadata:"+key, value)
}

func (s *FlatFileStore) DeleteConfig(_ context.Context, key string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	s.kvMu.Lock()
	defer s.kvMu.Unlock()

	kv, err := s.readKVFile(s.configKVPath)
	if err != nil {
		return err
	}
	delete(kv, key)
	return s.writeKVFile(s.configKVPath, kv)
}

func (s *FlatFileStore) GetCustomStatuses(_ context.Context) ([]string, error) {
	return nil, nil
}

func (s *FlatFileStore) GetCustomStatusesDetailed(_ context.Context) ([]types.CustomStatus, error) {
	return nil, nil
}

func (s *FlatFileStore) GetCustomTypes(_ context.Context) ([]string, error) {
	return nil, nil
}

func (s *FlatFileStore) GetInfraTypes(_ context.Context) map[string]bool {
	return map[string]bool{
		"gate":          true,
		"molecule":      true,
		"merge-request": true,
	}
}

func (s *FlatFileStore) IsInfraTypeCtx(_ context.Context, t types.IssueType) bool {
	infra := map[string]bool{
		"gate":          true,
		"molecule":      true,
		"merge-request": true,
	}
	return infra[string(t)]
}

// ── AdvancedQueryStore ───────────────────────────────────────────────

func (s *FlatFileStore) GetRepoMtime(_ context.Context, repoPath string) (int64, error) {
	if err := s.checkClosed(); err != nil {
		return 0, err
	}
	kv, err := s.readKVFile(s.repoMtimePath)
	if err != nil {
		return 0, err
	}
	v := kv[repoPath]
	if v == "" {
		return 0, nil
	}
	var n int64
	_, _ = fmt.Sscanf(v, "%d", &n)
	return n, nil
}

func (s *FlatFileStore) SetRepoMtime(_ context.Context, repoPath, _ string, mtimeNs int64) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	return s.setKV(s.repoMtimePath, repoPath, fmt.Sprintf("%d", mtimeNs))
}

func (s *FlatFileStore) ClearRepoMtime(_ context.Context, repoPath string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	kv, err := s.readKVFile(s.repoMtimePath)
	if err != nil {
		return err
	}
	delete(kv, repoPath)
	return s.writeKVFile(s.repoMtimePath, kv)
}

func (s *FlatFileStore) GetMoleculeProgress(_ context.Context, moleculeID string) (*types.MoleculeProgressStats, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}
	stats := &types.MoleculeProgressStats{MoleculeID: moleculeID}
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type == "parent-child" && dep.DependsOnID == moleculeID {
				stats.Total++
				if issue.Status == types.StatusClosed {
					stats.Completed++
					if issue.ClosedAt != nil {
						if stats.FirstClosed == nil || issue.ClosedAt.Before(*stats.FirstClosed) {
							stats.FirstClosed = issue.ClosedAt
						}
						if stats.LastClosed == nil || issue.ClosedAt.After(*stats.LastClosed) {
							stats.LastClosed = issue.ClosedAt
						}
					}
				} else if issue.Status == types.StatusInProgress {
					stats.InProgress++
					if stats.CurrentStepID == "" {
						stats.CurrentStepID = issue.ID
					}
				}
				break
			}
		}
	}
	// Get molecule title
	if mol, err := s.readIssue(moleculeID); err == nil {
		stats.MoleculeTitle = mol.Title
	}
	return stats, nil
}

func (s *FlatFileStore) GetMoleculeLastActivity(_ context.Context, moleculeID string) (*types.MoleculeLastActivity, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}
	result := &types.MoleculeLastActivity{MoleculeID: moleculeID}
	var latest time.Time
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type == "parent-child" && dep.DependsOnID == moleculeID {
				if issue.UpdatedAt.After(latest) {
					latest = issue.UpdatedAt
					result.LastActivity = latest
					result.Source = "step_updated"
					result.SourceStepID = issue.ID
				}
				break
			}
		}
	}
	return result, nil
}

func (s *FlatFileStore) GetStaleIssues(_ context.Context, filter types.StaleFilter) ([]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().AddDate(0, 0, -filter.Days)
	var result []*types.Issue
	for _, issue := range all {
		if filter.Status != "" && string(issue.Status) != filter.Status {
			continue
		} else if filter.Status == "" && issue.Status == types.StatusClosed {
			continue
		}
		if issue.UpdatedAt.Before(cutoff) {
			result = append(result, issue)
			if filter.Limit > 0 && len(result) >= filter.Limit {
				break
			}
		}
	}
	return result, nil
}

// ── CompactionStore ──────────────────────────────────────────────────

func (s *FlatFileStore) CheckEligibility(_ context.Context, _ string, _ int) (bool, string, error) {
	return false, "flat-file backend does not compact", nil
}

func (s *FlatFileStore) ApplyCompaction(_ context.Context, issueID string, tier int, originalSize int, compactedSize int, commitHash string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	issue, err := s.readIssue(issueID)
	if err != nil {
		return err
	}
	issue.CompactionLevel = tier
	now := time.Now().UTC()
	issue.CompactedAt = &now
	issue.CompactedAtCommit = &commitHash
	issue.OriginalSize = originalSize
	issue.UpdatedAt = now
	return s.writeIssue(issue)
}

func (s *FlatFileStore) GetTier1Candidates(_ context.Context) ([]*types.CompactionCandidate, error) {
	return nil, nil
}

func (s *FlatFileStore) GetTier2Candidates(_ context.Context) ([]*types.CompactionCandidate, error) {
	return nil, nil
}
