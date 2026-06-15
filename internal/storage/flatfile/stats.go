package flatfile

import (
	"context"

	"github.com/steveyegge/beads/internal/types"
)

// GetStatistics returns aggregate metrics across all issues.
func (s *FlatFileStore) GetStatistics(_ context.Context) (*types.Statistics, error) {
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

	stats := &types.Statistics{}

	var totalLead float64
	var closedWithLead int

	for _, issue := range all {
		stats.TotalIssues++

		switch issue.Status {
		case types.StatusOpen:
			stats.OpenIssues++
		case types.StatusInProgress:
			stats.InProgressIssues++
		case types.StatusClosed:
			stats.ClosedIssues++
			if issue.ClosedAt != nil && !issue.CreatedAt.IsZero() {
				lead := issue.ClosedAt.Sub(issue.CreatedAt).Hours()
				totalLead += lead
				closedWithLead++
			}
		case types.StatusBlocked:
			stats.BlockedIssues++
		case types.StatusDeferred:
			stats.DeferredIssues++
		}

		if issue.Pinned {
			stats.PinnedIssues++
		}

		// Ready: open/in_progress with no open blockers
		if isWorkableStatus(issue.Status) && !isDeferred(issue) && !hasOpenBlocker(issue, statusMap) {
			stats.ReadyIssues++
		}
	}

	// Count epics eligible for closure
	children := make(map[string][]*types.Issue)
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type == "parent-child" {
				children[dep.DependsOnID] = append(children[dep.DependsOnID], issue)
			}
		}
	}
	for _, issue := range all {
		if issue.IssueType != "epic" || issue.Status == types.StatusClosed {
			continue
		}
		kids := children[issue.ID]
		if len(kids) == 0 {
			continue
		}
		allClosed := true
		for _, k := range kids {
			if k.Status != types.StatusClosed {
				allClosed = false
				break
			}
		}
		if allClosed {
			stats.EpicsEligibleForClosure++
		}
	}

	if closedWithLead > 0 {
		stats.AverageLeadTime = totalLead / float64(closedWithLead)
	}

	return stats, nil
}
