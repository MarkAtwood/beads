package flatfile

import (
	"context"

	"github.com/steveyegge/beads/internal/types"
)

// GetStatistics returns aggregate metrics across all issues. Mirrors
// issueops.GetStatisticsInTx: six status counts, BlockedIssues from the
// is_blocked projection (active issues with an active blocker), and
// ReadyIssues = OpenIssues - BlockedIssues clamped at zero.
func (s *FlatFileStore) GetStatistics(_ context.Context) (*types.Statistics, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
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
		case types.StatusDeferred:
			stats.DeferredIssues++
		case types.StatusBlocked, types.StatusPinned, types.StatusHooked:
			// No dedicated status counter, matching the SQL reference: the
			// blocked count comes from the is_blocked projection below and
			// PinnedIssues from the pinned column flag.
		}

		if issue.Pinned {
			stats.PinnedIssues++
		}
	}

	// BlockedIssues mirrors `is_blocked = 1 AND status NOT IN (closed, pinned)`;
	// computeBlockedSet only ever marks active (non-closed, non-pinned) issues.
	stats.BlockedIssues = len(computeBlockedSet(all))
	stats.ReadyIssues = stats.OpenIssues - stats.BlockedIssues
	if stats.ReadyIssues < 0 {
		stats.ReadyIssues = 0
	}

	// Count epics eligible for closure
	children := make(map[string][]*types.Issue)
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type == types.DepParentChild {
				children[dep.DependsOnID] = append(children[dep.DependsOnID], issue)
			}
		}
	}
	for _, issue := range all {
		if issue.IssueType != types.TypeEpic || issue.Status == types.StatusClosed {
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
