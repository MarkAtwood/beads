package flatfile

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// SQL reference semantics: every AddIssueComment inserts a distinct row —
// comments created in the same clock tick can never overwrite each other.
// The flatfile mechanism is the comment ID embedded in the filename; two
// distinct IDs always yield two distinct paths regardless of timestamp.
func TestAddIssueCommentFilenameIncludesID(t *testing.T) {
	s := newTestStore(t)

	issue := &types.Issue{ID: "cmt-1", Title: "Comments"}
	if err := s.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	c1, err := s.AddIssueComment(ctx, "cmt-1", "alice", "first")
	if err != nil {
		t.Fatalf("AddIssueComment: %v", err)
	}
	c2, err := s.AddIssueComment(ctx, "cmt-1", "bob", "second")
	if err != nil {
		t.Fatalf("AddIssueComment: %v", err)
	}

	dir, err := safePath(s.commentsDir, "cmt-1", "")
	if err != nil {
		t.Fatalf("safePath: %v", err)
	}
	entries, err := readDirSafe(dir)
	if err != nil {
		t.Fatalf("readDirSafe: %v", err)
	}
	for _, c := range []*types.Comment{c1, c2} {
		found := false
		for _, e := range entries {
			if strings.Contains(e.Name(), sanitizeFilenameComponent(c.ID)) {
				found = true
			}
		}
		if !found {
			t.Errorf("no comment file embeds ID %q; timestamp-only names collide within one clock tick", c.ID)
		}
	}

	comments, err := s.GetIssueComments(ctx, "cmt-1")
	if err != nil {
		t.Fatalf("GetIssueComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(comments))
	}
}
