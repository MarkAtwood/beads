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

// SQL parity: invalid label regex/glob must surface as an error, not silently
// match everything (nil regex) or nothing (glob ErrBadPattern swallowed).
func TestSearchIssuesInvalidLabelRegexErrors(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	_, err := s.SearchIssues(ctx, "", types.IssueFilter{LabelRegex: "tech-(debt"})
	if err == nil {
		t.Fatal("SearchIssues with unbalanced regex: want error, got nil")
	}
}

func TestSearchIssuesInvalidLabelPatternErrors(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	_, err := s.SearchIssues(ctx, "", types.IssueFilter{LabelPattern: "[a-"})
	if err == nil {
		t.Fatal("SearchIssues with malformed glob: want error, got nil")
	}
}

func TestSearchIssuesValidLabelRegexAndPattern(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	got, err := s.SearchIssues(ctx, "", types.IssueFilter{LabelRegex: "^sec"})
	if err != nil {
		t.Fatalf("SearchIssues LabelRegex: %v", err)
	}
	if len(got) != 1 || got[0].ID != "s-1" {
		t.Errorf("LabelRegex ^sec: got %d issues, want [s-1]", len(got))
	}

	got, err = s.SearchIssues(ctx, "", types.IssueFilter{LabelPattern: "u*"})
	if err != nil {
		t.Fatalf("SearchIssues LabelPattern: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("LabelPattern u*: got %d issues, want 2 (urgent, ui)", len(got))
	}
}

// SQL parity (sqlbuild/filter.go ParentID clause): --parent matches explicit
// parent-child deps AND implicit dotted-ID children that have no parent-child
// dep at all; a dotted-ID issue with a parent-child dep to a DIFFERENT parent
// must not match via the prefix fallback.
func TestSearchIssuesParentIDImplicitDottedChildren(t *testing.T) {
	s := newTestStore(t)

	for _, id := range []string{"p-1", "p-2", "p-1.1", "p-1.2", "p-1.3", "p-10"} {
		if err := s.CreateIssue(ctx, &types.Issue{ID: id, Title: id}, "tester"); err != nil {
			t.Fatalf("CreateIssue(%s): %v", id, err)
		}
	}
	// p-1.1: explicit child of p-1. p-1.2: no deps (implicit child).
	// p-1.3: dotted ID but explicitly parented under p-2 — must NOT match p-1.
	for _, d := range []struct{ child, parent string }{
		{"p-1.1", "p-1"}, {"p-1.3", "p-2"},
	} {
		dep := &types.Dependency{IssueID: d.child, DependsOnID: d.parent, Type: types.DepParentChild}
		if err := s.AddDependency(ctx, dep, "tester"); err != nil {
			t.Fatalf("AddDependency(%s->%s): %v", d.child, d.parent, err)
		}
	}

	parent := "p-1"
	got, err := s.SearchIssues(ctx, "", types.IssueFilter{ParentID: &parent})
	if err != nil {
		t.Fatalf("SearchIssues: %v", err)
	}
	ids := make(map[string]bool, len(got))
	for _, is := range got {
		ids[is.ID] = true
	}
	want := []string{"p-1.1", "p-1.2"}
	if len(got) != len(want) {
		t.Errorf("ParentID=p-1: got %d issues %v, want %v", len(got), ids, want)
	}
	for _, w := range want {
		if !ids[w] {
			t.Errorf("ParentID=p-1: missing %s", w)
		}
	}
	// p-10 shares the "p-1" string prefix but not the "p-1." dotted prefix.
	if ids["p-10"] || ids["p-1.3"] {
		t.Errorf("ParentID=p-1: matched wrong issues: %v", ids)
	}
}

// SQL parity (issueops countGroupForTablesInTx): unknown groupBy — including
// the SQL column name "issue_type" — errors instead of returning a bucket.
func TestCountIssuesByGroupUnsupportedErrors(t *testing.T) {
	s := newTestStore(t)
	seedIssues(t, s)

	for _, bad := range []string{"prio", "issue_type", ""} {
		if _, err := s.CountIssuesByGroup(ctx, types.IssueFilter{}, bad); err == nil {
			t.Errorf("CountIssuesByGroup(%q): want error, got nil", bad)
		}
	}

	// Errors on an empty store too (SQL errors regardless of row count).
	empty := newTestStore(t)
	if _, err := empty.CountIssuesByGroup(ctx, types.IssueFilter{}, "prio"); err == nil {
		t.Error("CountIssuesByGroup on empty store: want error, got nil")
	}

	// "type" remains the supported spelling.
	counts, err := s.CountIssuesByGroup(ctx, types.IssueFilter{}, "type")
	if err != nil {
		t.Fatalf("CountIssuesByGroup(type): %v", err)
	}
	if counts["bug"] != 2 {
		t.Errorf("type=bug count = %d, want 2", counts["bug"])
	}
}
