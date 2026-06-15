package flatfile

import (
	"context"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

var ctx = context.Background()

func TestCreateAndGetIssue(t *testing.T) {
	s := newTestStore(t)

	issue := &types.Issue{
		ID:          "test-1",
		Title:       "Test issue",
		Description: "A test issue",
		Priority:    2,
	}

	if err := s.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	got, err := s.GetIssue(ctx, "test-1")
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}

	if got.Title != "Test issue" {
		t.Errorf("Title = %q, want %q", got.Title, "Test issue")
	}
	if got.Status != types.StatusOpen {
		t.Errorf("Status = %q, want %q", got.Status, types.StatusOpen)
	}
	if got.CreatedBy != "tester" {
		t.Errorf("CreatedBy = %q, want %q", got.CreatedBy, "tester")
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
}

func TestCreateIssueDuplicate(t *testing.T) {
	s := newTestStore(t)

	issue := &types.Issue{ID: "dup-1", Title: "First"}
	if err := s.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	issue2 := &types.Issue{ID: "dup-1", Title: "Second"}
	if err := s.CreateIssue(ctx, issue2, "tester"); err == nil {
		t.Error("CreateIssue duplicate: expected error, got nil")
	}
}

func TestCreateIssues(t *testing.T) {
	s := newTestStore(t)

	issues := []*types.Issue{
		{ID: "batch-1", Title: "First"},
		{ID: "batch-2", Title: "Second"},
		{ID: "batch-3", Title: "Third"},
	}

	if err := s.CreateIssues(ctx, issues, "tester"); err != nil {
		t.Fatalf("CreateIssues: %v", err)
	}

	for _, id := range []string{"batch-1", "batch-2", "batch-3"} {
		if _, err := s.GetIssue(ctx, id); err != nil {
			t.Errorf("GetIssue(%s): %v", id, err)
		}
	}
}

func TestGetIssueNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetIssue(ctx, "nonexistent")
	if err != storage.ErrNotFound {
		t.Errorf("GetIssue missing = %v, want ErrNotFound", err)
	}
}

func TestGetIssueByExternalRef(t *testing.T) {
	s := newTestStore(t)

	ref := "gh-42"
	issue := &types.Issue{ID: "ext-1", Title: "External", ExternalRef: &ref}
	s.CreateIssue(ctx, issue, "tester")

	got, err := s.GetIssueByExternalRef(ctx, "gh-42")
	if err != nil {
		t.Fatalf("GetIssueByExternalRef: %v", err)
	}
	if got.ID != "ext-1" {
		t.Errorf("ID = %q, want %q", got.ID, "ext-1")
	}

	_, err = s.GetIssueByExternalRef(ctx, "gh-999")
	if err != storage.ErrNotFound {
		t.Errorf("missing ref = %v, want ErrNotFound", err)
	}
}

func TestGetIssuesByIDs(t *testing.T) {
	s := newTestStore(t)

	s.CreateIssue(ctx, &types.Issue{ID: "a-1", Title: "A"}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "a-2", Title: "B"}, "tester")

	got, err := s.GetIssuesByIDs(ctx, []string{"a-1", "missing", "a-2"})
	if err != nil {
		t.Fatalf("GetIssuesByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
}

func TestUpdateIssue(t *testing.T) {
	s := newTestStore(t)

	s.CreateIssue(ctx, &types.Issue{ID: "upd-1", Title: "Original", Priority: 1}, "tester")

	err := s.UpdateIssue(ctx, "upd-1", map[string]interface{}{
		"title":    "Updated",
		"priority": 3,
	}, "tester")
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	got, _ := s.GetIssue(ctx, "upd-1")
	if got.Title != "Updated" {
		t.Errorf("Title = %q, want %q", got.Title, "Updated")
	}
	if got.Priority != 3 {
		t.Errorf("Priority = %d, want 3", got.Priority)
	}
}

func TestUpdateIssueNotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.UpdateIssue(ctx, "missing", map[string]interface{}{"title": "x"}, "tester")
	if err != storage.ErrNotFound {
		t.Errorf("UpdateIssue missing = %v, want ErrNotFound", err)
	}
}

func TestUpdateIssueType(t *testing.T) {
	s := newTestStore(t)

	s.CreateIssue(ctx, &types.Issue{ID: "type-1", Title: "Task", IssueType: "task"}, "tester")

	if err := s.UpdateIssueType(ctx, "type-1", "epic", "tester"); err != nil {
		t.Fatalf("UpdateIssueType: %v", err)
	}

	got, _ := s.GetIssue(ctx, "type-1")
	if got.IssueType != "epic" {
		t.Errorf("IssueType = %q, want %q", got.IssueType, "epic")
	}
}

func TestCloseIssue(t *testing.T) {
	s := newTestStore(t)

	s.CreateIssue(ctx, &types.Issue{ID: "close-1", Title: "Will close"}, "tester")

	if err := s.CloseIssue(ctx, "close-1", "done", "closer", "session-123"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}

	got, _ := s.GetIssue(ctx, "close-1")
	if got.Status != types.StatusClosed {
		t.Errorf("Status = %q, want %q", got.Status, types.StatusClosed)
	}
	if got.ClosedAt == nil {
		t.Error("ClosedAt is nil")
	}
	if got.CloseReason != "done" {
		t.Errorf("CloseReason = %q, want %q", got.CloseReason, "done")
	}
	if got.ClosedBySession != "session-123" {
		t.Errorf("ClosedBySession = %q, want %q", got.ClosedBySession, "session-123")
	}
}

func TestReopenIssue(t *testing.T) {
	s := newTestStore(t)

	s.CreateIssue(ctx, &types.Issue{ID: "reopen-1", Title: "Reopen me"}, "tester")
	s.CloseIssue(ctx, "reopen-1", "done", "closer", "sess")

	if err := s.ReopenIssue(ctx, "reopen-1", "not done", "opener"); err != nil {
		t.Fatalf("ReopenIssue: %v", err)
	}

	got, _ := s.GetIssue(ctx, "reopen-1")
	if got.Status != types.StatusOpen {
		t.Errorf("Status = %q, want %q", got.Status, types.StatusOpen)
	}
	if got.ClosedAt != nil {
		t.Error("ClosedAt should be nil after reopen")
	}
}

func TestDeleteIssue(t *testing.T) {
	s := newTestStore(t)

	s.CreateIssue(ctx, &types.Issue{ID: "del-1", Title: "Delete me"}, "tester")

	if err := s.DeleteIssue(ctx, "del-1"); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}

	_, err := s.GetIssue(ctx, "del-1")
	if err != storage.ErrNotFound {
		t.Errorf("after delete: %v, want ErrNotFound", err)
	}
}

func TestDeleteIssueNotFound(t *testing.T) {
	s := newTestStore(t)

	err := s.DeleteIssue(ctx, "nonexistent")
	if err != storage.ErrNotFound {
		t.Errorf("DeleteIssue missing = %v, want ErrNotFound", err)
	}
}

func TestUpdatePreservesTimestamps(t *testing.T) {
	s := newTestStore(t)

	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	issue := &types.Issue{
		ID:        "ts-1",
		Title:     "Timestamps",
		CreatedAt: created,
		UpdatedAt: created,
		CreatedBy: "original",
	}
	s.CreateIssue(ctx, issue, "original")

	s.UpdateIssue(ctx, "ts-1", map[string]interface{}{"title": "Changed"}, "updater")

	got, _ := s.GetIssue(ctx, "ts-1")
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt changed: got %v, want %v", got.CreatedAt, created)
	}
	if got.CreatedBy != "original" {
		t.Errorf("CreatedBy changed: got %q, want %q", got.CreatedBy, "original")
	}
	if !got.UpdatedAt.After(created) {
		t.Error("UpdatedAt should be after original timestamp")
	}
}

func TestOperationsAfterClose(t *testing.T) {
	s := newTestStore(t)
	s.Close()

	if err := s.CreateIssue(ctx, &types.Issue{ID: "x"}, ""); err == nil {
		t.Error("CreateIssue after Close: expected error")
	}
	if _, err := s.GetIssue(ctx, "x"); err == nil {
		t.Error("GetIssue after Close: expected error")
	}
	if err := s.UpdateIssue(ctx, "x", nil, ""); err == nil {
		t.Error("UpdateIssue after Close: expected error")
	}
	if err := s.DeleteIssue(ctx, "x"); err == nil {
		t.Error("DeleteIssue after Close: expected error")
	}
}
