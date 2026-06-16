package flatfile

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func seedIssues(t *testing.T, s *FlatFileStore) {
	t.Helper()
	ctx := context.Background()
	p0, p1, p2 := 0, 1, 2
	issues := []*types.Issue{
		{ID: "s-1", Title: "Bug in auth", Description: "Login fails", Status: types.StatusOpen, Priority: p0, IssueType: "bug", Assignee: "alice", Labels: []string{"security", "urgent"}},
		{ID: "s-2", Title: "Add dark mode", Description: "User request", Status: types.StatusOpen, Priority: p2, IssueType: "feature", Labels: []string{"ui"}},
		{ID: "s-3", Title: "Refactor DB layer", Status: types.StatusInProgress, Priority: p1, IssueType: "task", Assignee: "bob"},
		{ID: "s-4", Title: "Closed old bug", Status: types.StatusClosed, Priority: p1, IssueType: "bug"},
		{ID: "s-5", Title: "Epic: Q3 roadmap", Status: types.StatusOpen, Priority: p0, IssueType: "epic"},
	}
	for _, issue := range issues {
		if err := s.CreateIssue(ctx, issue, "seeder"); err != nil {
			t.Fatalf("seed CreateIssue(%s): %v", issue.ID, err)
		}
	}
	_ = p0
}

func TestSearchIssuesNoFilter(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	results, err := s.SearchIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(results) != 5 {
		t.Errorf("len = %d, want 5", len(results))
	}
}

func TestSearchIssuesTextQuery(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	results, err := s.SearchIssues(ctx, "auth", types.IssueFilter{})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("query 'auth': len = %d, want 1", len(results))
	}
}

func TestSearchIssuesStatusFilter(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	open := types.StatusOpen
	results, err := s.SearchIssues(ctx, "", types.IssueFilter{Status: &open})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("status=open: len = %d, want 3", len(results))
	}
}

func TestSearchIssuesMultiStatus(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	results, err := s.SearchIssues(ctx, "", types.IssueFilter{
		Statuses: []types.Status{types.StatusOpen, types.StatusInProgress},
	})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(results) != 4 {
		t.Errorf("multi-status: len = %d, want 4", len(results))
	}
}

func TestSearchIssuesPriorityFilter(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	p := 0
	results, err := s.SearchIssues(ctx, "", types.IssueFilter{Priority: &p})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("priority=0: len = %d, want 2", len(results))
	}
}

func TestSearchIssuesTypeFilter(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	bug := types.IssueType("bug")
	results, err := s.SearchIssues(ctx, "", types.IssueFilter{IssueType: &bug})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("type=bug: len = %d, want 2", len(results))
	}
}

func TestSearchIssuesLabelFilter(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	results, err := s.SearchIssues(ctx, "", types.IssueFilter{Labels: []string{"security"}})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("label=security: len = %d, want 1", len(results))
	}
}

func TestSearchIssuesAssigneeFilter(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	a := "alice"
	results, err := s.SearchIssues(ctx, "", types.IssueFilter{Assignee: &a})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("assignee=alice: len = %d, want 1", len(results))
	}
}

func TestSearchIssuesNoAssignee(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	results, err := s.SearchIssues(ctx, "", types.IssueFilter{NoAssignee: true})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("no assignee: len = %d, want 3", len(results))
	}
}

func TestSearchIssuesLimit(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	results, err := s.SearchIssues(ctx, "", types.IssueFilter{Limit: 2})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("limit=2: len = %d, want 2", len(results))
	}
}

func TestSearchIssuesExcludeStatus(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	results, err := s.SearchIssues(ctx, "", types.IssueFilter{
		ExcludeStatus: []types.Status{types.StatusClosed},
	})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	if len(results) != 4 {
		t.Errorf("exclude closed: len = %d, want 4", len(results))
	}
}

func TestCountIssues(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	count, err := s.CountIssues(ctx, "", types.IssueFilter{})
	if err != nil {
		t.Fatalf("CountIssues: %v", err)
	}
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
}

func TestCountIssuesByGroup(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	counts, err := s.CountIssuesByGroup(ctx, types.IssueFilter{}, "status")
	if err != nil {
		t.Fatalf("CountIssuesByGroup: %v", err)
	}
	if counts["open"] != 3 {
		t.Errorf("open count = %d, want 3", counts["open"])
	}
	if counts["closed"] != 1 {
		t.Errorf("closed count = %d, want 1", counts["closed"])
	}
}

func TestSearchIssuesWithCounts(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	results, err := s.SearchIssuesWithCounts(ctx, "", types.IssueFilter{Limit: 2})
	if err != nil {
		t.Fatalf("SearchIssuesWithCounts: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("len = %d, want 2", len(results))
	}
}
