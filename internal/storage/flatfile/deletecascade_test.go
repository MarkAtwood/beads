package flatfile

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// Oracle: the SQL backends cascade comments and events with the issue row via
// fk_comments_issue/fk_events_issue ON DELETE CASCADE, so a deleted issue's
// ID carries no history if it is later reused.

func seedIssueWithChildren(t *testing.T, s *FlatFileStore, id, sourceRepo string) {
	t.Helper()
	ctx := context.Background()
	issue := &types.Issue{ID: id, Title: "t", Status: types.StatusOpen, Priority: 1, IssueType: types.TypeTask, SourceRepo: sourceRepo}
	if err := s.CreateIssue(ctx, issue, "a"); err != nil {
		t.Fatalf("CreateIssue %s: %v", id, err)
	}
	if _, err := s.ImportIssueComment(ctx, id, "a", "hello", time.Now().UTC()); err != nil {
		t.Fatalf("ImportIssueComment %s: %v", id, err)
	}
	assertChildren(t, s, id, true)
}

func assertChildren(t *testing.T, s *FlatFileStore, id string, want bool) {
	t.Helper()
	ctx := context.Background()
	events, err := s.GetEvents(ctx, id, 0)
	if err != nil {
		t.Fatalf("GetEvents %s: %v", id, err)
	}
	comments, err := s.GetIssueComments(ctx, id)
	if err != nil {
		t.Fatalf("GetIssueComments %s: %v", id, err)
	}
	if got := len(events) > 0; got != want {
		t.Errorf("%s events present = %v, want %v (events: %d)", id, got, want, len(events))
	}
	if got := len(comments) > 0; got != want {
		t.Errorf("%s comments present = %v, want %v (comments: %d)", id, got, want, len(comments))
	}
}

func TestDeleteIssueCascadesChildFiles(t *testing.T) {
	s := newTestStore(t)
	seedIssueWithChildren(t, s, "bd-1", "")

	if err := s.DeleteIssue(context.Background(), "bd-1"); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	assertChildren(t, s, "bd-1", false)
}

func TestDeleteIssuesCascadesChildFiles(t *testing.T) {
	s := newTestStore(t)
	seedIssueWithChildren(t, s, "bd-1", "")

	res, err := s.DeleteIssues(context.Background(), []string{"bd-1"}, false, false, false)
	if err != nil {
		t.Fatalf("DeleteIssues: %v", err)
	}
	if res.DeletedCount != 1 {
		t.Fatalf("DeletedCount = %d, want 1", res.DeletedCount)
	}
	assertChildren(t, s, "bd-1", false)
}

func TestDeleteIssuesBySourceRepoCascadesChildFiles(t *testing.T) {
	s := newTestStore(t)
	seedIssueWithChildren(t, s, "bd-1", "repo-x")
	seedIssueWithChildren(t, s, "bd-2", "repo-y")

	n, err := s.DeleteIssuesBySourceRepo(context.Background(), "repo-x")
	if err != nil {
		t.Fatalf("DeleteIssuesBySourceRepo: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted = %d, want 1", n)
	}
	assertChildren(t, s, "bd-1", false)
	assertChildren(t, s, "bd-2", true) // other repo untouched
}
