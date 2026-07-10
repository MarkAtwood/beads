package flatfile

import (
	"context"
	"sort"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// GetReadyWork returns open issues with no open blocking dependencies.
func (s *FlatFileStore) GetReadyWork(ctx context.Context, filter types.WorkFilter) ([]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	statusMap := make(map[string]types.Status, len(all))
	for _, issue := range all {
		statusMap[issue.ID] = issue.Status
	}

	var result []*types.Issue
	for _, issue := range all {
		if !isWorkableStatus(issue.Status) {
			continue
		}
		if issue.Ephemeral && !filter.IncludeEphemeral {
			continue
		}
		if !filter.IncludeDeferred && isDeferred(issue) {
			continue
		}
		if !matchesWorkFilter(issue, filter) {
			continue
		}
		if hasOpenBlocker(issue, statusMap) {
			continue
		}
		result = append(result, issue)
	}

	sortReadyWork(result, filter.SortPolicy)

	if filter.Offset > 0 && filter.Offset < len(result) {
		result = result[filter.Offset:]
	} else if filter.Offset >= len(result) {
		return nil, nil
	}
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}

	return result, nil
}

// GetReadyWorkWithCounts is GetReadyWork plus dependency/comment counts.
func (s *FlatFileStore) GetReadyWorkWithCounts(ctx context.Context, filter types.WorkFilter) ([]*types.IssueWithCounts, error) {
	issues, err := s.GetReadyWork(ctx, filter)
	if err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	depCounts := make(map[string]int)
	parentMap := make(map[string]string)
	for _, a := range all {
		for _, dep := range a.Dependencies {
			if dep.Type == types.DepParentChild {
				parentMap[a.ID] = dep.DependsOnID
			} else {
				depCounts[dep.DependsOnID]++
			}
		}
	}

	var result []*types.IssueWithCounts
	for _, issue := range issues {
		dc := 0
		for _, dep := range issue.Dependencies {
			if dep.Type != types.DepParentChild {
				dc++
			}
		}
		cc, _ := s.countCommentFiles(issue.ID)
		wc := &types.IssueWithCounts{
			Issue:           issue,
			DependencyCount: dc,
			DependentCount:  depCounts[issue.ID],
			CommentCount:    cc,
		}
		if p, ok := parentMap[issue.ID]; ok {
			wc.Parent = &p
		}
		result = append(result, wc)
	}
	return result, nil
}

// CountReadyWork returns the total ready-work count for the filter,
// ignoring pagination, satisfying storage.ReadyWorkCounter. It equals
// len(GetReadyWork(filter with Limit=0)); with an in-memory scan there is
// no cheaper count than materializing the ready set.
func (s *FlatFileStore) CountReadyWork(ctx context.Context, filter types.WorkFilter) (int, error) {
	filter.Limit = 0
	filter.Offset = 0
	issues, err := s.GetReadyWork(ctx, filter)
	if err != nil {
		return 0, err
	}
	return len(issues), nil
}

// GetBlockedIssues returns open issues that have at least one open dependency.
func (s *FlatFileStore) GetBlockedIssues(_ context.Context, filter types.WorkFilter) ([]*types.BlockedIssue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	statusMap := make(map[string]types.Status, len(all))
	for _, issue := range all {
		statusMap[issue.ID] = issue.Status
	}

	var result []*types.BlockedIssue
	for _, issue := range all {
		if !isWorkableStatus(issue.Status) {
			continue
		}
		if !matchesWorkFilter(issue, filter) {
			continue
		}

		var blockers []string
		for _, dep := range issue.Dependencies {
			if dep.Type == types.DepParentChild {
				continue
			}
			if st, ok := statusMap[dep.DependsOnID]; ok && st != types.StatusClosed {
				blockers = append(blockers, dep.DependsOnID)
			}
		}

		if len(blockers) > 0 {
			result = append(result, &types.BlockedIssue{
				Issue:          *issue,
				BlockedByCount: len(blockers),
				BlockedBy:      blockers,
			})
		}
	}

	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

// GetEpicsEligibleForClosure returns epics where all children are closed.
func (s *FlatFileStore) GetEpicsEligibleForClosure(_ context.Context) ([]*types.EpicStatus, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	// Build parent → children map.
	children := make(map[string][]*types.Issue)
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type == types.DepParentChild {
				children[dep.DependsOnID] = append(children[dep.DependsOnID], issue)
			}
		}
	}

	var result []*types.EpicStatus
	for _, issue := range all {
		if issue.IssueType != types.TypeEpic || issue.Status == types.StatusClosed {
			continue
		}
		kids := children[issue.ID]
		if len(kids) == 0 {
			continue
		}
		closed := 0
		for _, kid := range kids {
			if kid.Status == types.StatusClosed {
				closed++
			}
		}
		result = append(result, &types.EpicStatus{
			Epic:             issue,
			TotalChildren:    len(kids),
			ClosedChildren:   closed,
			EligibleForClose: closed == len(kids),
		})
	}
	return result, nil
}

func isWorkableStatus(s types.Status) bool {
	return s == types.StatusOpen || s == types.StatusInProgress || s == types.StatusBlocked
}

func isDeferred(issue *types.Issue) bool {
	if issue.Status == types.StatusDeferred {
		return true
	}
	if issue.DeferUntil != nil && issue.DeferUntil.After(time.Now()) {
		return true
	}
	return false
}

func hasOpenBlocker(issue *types.Issue, statusMap map[string]types.Status) bool {
	for _, dep := range issue.Dependencies {
		if dep.Type == types.DepParentChild {
			continue
		}
		if st, ok := statusMap[dep.DependsOnID]; ok && st != types.StatusClosed {
			return true
		}
	}
	return false
}

func matchesWorkFilter(issue *types.Issue, f types.WorkFilter) bool {
	if f.Status != "" && issue.Status != f.Status {
		return false
	}
	if f.Type != "" && string(issue.IssueType) != f.Type {
		return false
	}
	if f.Priority != nil && issue.Priority != *f.Priority {
		return false
	}
	if f.Assignee != nil && issue.Assignee != *f.Assignee {
		return false
	}
	if f.Unassigned && issue.Assignee != "" {
		return false
	}
	if len(f.Labels) > 0 {
		labelSet := makeStringSet(issue.Labels)
		for _, l := range f.Labels {
			if !labelSet[l] {
				return false
			}
		}
	}
	if len(f.ExcludeLabels) > 0 {
		labelSet := makeStringSet(issue.Labels)
		for _, l := range f.ExcludeLabels {
			if labelSet[l] {
				return false
			}
		}
	}
	for _, et := range f.ExcludeTypes {
		if issue.IssueType == et {
			return false
		}
	}
	if f.ParentID != nil {
		found := false
		for _, dep := range issue.Dependencies {
			if dep.Type == types.DepParentChild && dep.DependsOnID == *f.ParentID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sortReadyWork(issues []*types.Issue, policy types.SortPolicy) {
	switch policy {
	case types.SortPolicyOldest:
		sort.Slice(issues, func(i, j int) bool {
			return issues[i].CreatedAt.Before(issues[j].CreatedAt)
		})
	case types.SortPolicyPriority, types.SortPolicyHybrid, "":
		sort.Slice(issues, func(i, j int) bool {
			if issues[i].Priority != issues[j].Priority {
				return issues[i].Priority < issues[j].Priority
			}
			return issues[i].CreatedAt.Before(issues[j].CreatedAt)
		})
	}
}
