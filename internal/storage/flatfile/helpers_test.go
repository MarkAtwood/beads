package flatfile

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestValidateID(t *testing.T) {
	valid := []string{"proj-abc", "proj-abc.1", "PROJ-123", "a-b-c"}
	for _, id := range valid {
		if err := validateID(id); err != nil {
			t.Errorf("validateID(%q) = %v, want nil", id, err)
		}
	}

	invalid := []string{
		"",
		"../etc/passwd",
		"foo/bar",
		"foo\\bar",
		".hidden",
		"..sneaky",
		"../../escape",
	}
	for _, id := range invalid {
		if err := validateID(id); err == nil {
			t.Errorf("validateID(%q) = nil, want error", id)
		}
	}
}

func TestPathTraversalBlocked(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	malicious := []string{
		"../../etc/passwd",
		"../sibling",
		"foo/../../escape",
	}

	for _, id := range malicious {
		// CreateIssue should reject
		err := s.CreateIssue(ctx, &types.Issue{ID: id, Title: "evil"}, "attacker")
		if err == nil {
			t.Errorf("CreateIssue(%q) should be rejected", id)
		}

		// GetIssue should reject
		_, err = s.GetIssue(ctx, id)
		if err == nil {
			t.Errorf("GetIssue(%q) should be rejected", id)
		}

		// DeleteIssue should reject
		err = s.DeleteIssue(ctx, id)
		if err == nil {
			t.Errorf("DeleteIssue(%q) should be rejected", id)
		}
	}
}
