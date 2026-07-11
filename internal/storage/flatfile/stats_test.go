package flatfile

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// SQL reference: issueops.GetStatisticsInTx (and the dolt/embedded variants)
// compute only the six status counts, BlockedIssues, and ReadyIssues.
// AverageLeadTime and EpicsEligibleForClosure are never filled, and bd status
// prints them only when > 0 — so flatfile must leave them zero even when the
// store holds closed issues with lead time and epics with all-closed children.
func TestGetStatisticsMatchesSQLReference(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	created := time.Now().Add(-72 * time.Hour)
	closed := time.Now().Add(-1 * time.Hour)
	s.CreateIssue(ctx, &types.Issue{ID: "gs-c1", Title: "Closed with lead", Status: types.StatusClosed,
		CreatedAt: created, ClosedAt: &closed}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "gs-epic", Title: "Epic", IssueType: types.TypeEpic, Status: types.StatusOpen}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "gs-epic.1", Title: "Closed child", Status: types.StatusClosed}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "gs-epic.1", DependsOnID: "gs-epic", Type: types.DepParentChild}, "tester")

	stats, err := s.GetStatistics(ctx)
	if err != nil {
		t.Fatalf("GetStatistics: %v", err)
	}
	if stats.AverageLeadTime != 0 {
		t.Errorf("AverageLeadTime = %v, want 0 (no SQL backend fills it)", stats.AverageLeadTime)
	}
	if stats.EpicsEligibleForClosure != 0 {
		t.Errorf("EpicsEligibleForClosure = %d, want 0 (no SQL backend fills it)", stats.EpicsEligibleForClosure)
	}
	if stats.TotalIssues != 3 || stats.ClosedIssues != 2 || stats.OpenIssues != 1 {
		t.Errorf("counts = total %d closed %d open %d, want 3/2/1",
			stats.TotalIssues, stats.ClosedIssues, stats.OpenIssues)
	}
}
