package flatfile

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestAddAndGetDependency(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "a-1", Title: "Parent"}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "a-2", Title: "Child"}, "tester")

	dep := &types.Dependency{
		IssueID:     "a-2",
		DependsOnID: "a-1",
		Type:        "blocks",
	}
	if err := s.AddDependency(ctx, dep, "tester"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}

	// GetDependencies: a-2 depends on a-1
	deps, err := s.GetDependencies(ctx, "a-2")
	if err != nil {
		t.Fatalf("GetDependencies: %v", err)
	}
	if len(deps) != 1 || deps[0].ID != "a-1" {
		t.Errorf("GetDependencies = %v, want [a-1]", deps)
	}

	// GetDependents: a-1 is depended on by a-2
	dependents, err := s.GetDependents(ctx, "a-1")
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(dependents) != 1 || dependents[0].ID != "a-2" {
		t.Errorf("GetDependents = %v, want [a-2]", dependents)
	}
}

func TestAddDependencyIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "b-1", Title: "One"}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "b-2", Title: "Two"}, "tester")

	dep := &types.Dependency{IssueID: "b-2", DependsOnID: "b-1", Type: "blocks"}
	s.AddDependency(ctx, dep, "tester")
	s.AddDependency(ctx, dep, "tester") // second time is no-op

	issue, _ := s.GetIssue(ctx, "b-2")
	count := 0
	for _, d := range issue.Dependencies {
		if d.DependsOnID == "b-1" && d.Type == "blocks" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("duplicate deps: count = %d, want 1", count)
	}
}

func TestRemoveDependency(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "c-1", Title: "Target"}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "c-2", Title: "Source"}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "c-2", DependsOnID: "c-1", Type: "blocks"}, "tester")

	if err := s.RemoveDependency(ctx, "c-2", "c-1", "tester"); err != nil {
		t.Fatalf("RemoveDependency: %v", err)
	}

	deps, _ := s.GetDependencies(ctx, "c-2")
	if len(deps) != 0 {
		t.Errorf("after remove: len(deps) = %d, want 0", len(deps))
	}
}

func TestRemoveDependencyNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "d-1", Title: "Solo"}, "tester")

	// Mirrors issueops.RemoveDependencyInTx: removing a non-existent edge is
	// a silent no-op (pinned by the RemoveMissingAndUnblock conformance case).
	err := s.RemoveDependency(ctx, "d-1", "nonexistent", "tester")
	if err != nil {
		t.Errorf("RemoveDependency missing = %v, want nil (silent no-op)", err)
	}
}

func TestCountDependencies(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "e-1", Title: "A"}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "e-2", Title: "B"}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "e-3", Title: "C"}, "tester")

	s.AddDependency(ctx, &types.Dependency{IssueID: "e-3", DependsOnID: "e-1", Type: "blocks"}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "e-3", DependsOnID: "e-2", Type: "blocks"}, "tester")

	count, err := s.CountDependencies(ctx, "e-3")
	if err != nil {
		t.Fatalf("CountDependencies: %v", err)
	}
	if count != 2 {
		t.Errorf("CountDependencies = %d, want 2", count)
	}

	depCount, _ := s.CountDependents(ctx, "e-1")
	if depCount != 1 {
		t.Errorf("CountDependents(e-1) = %d, want 1", depCount)
	}
}

func TestGetDependencyTree(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "t-1", Title: "Root"}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "t-2", Title: "Mid"}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "t-3", Title: "Leaf"}, "tester")

	s.AddDependency(ctx, &types.Dependency{IssueID: "t-2", DependsOnID: "t-1", Type: "blocks"}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "t-3", DependsOnID: "t-2", Type: "blocks"}, "tester")

	// Forward: t-3 -> t-2 -> t-1
	tree, err := s.GetDependencyTree(ctx, "t-3", 10, false, false)
	if err != nil {
		t.Fatalf("GetDependencyTree: %v", err)
	}
	if len(tree) != 3 {
		t.Errorf("tree len = %d, want 3", len(tree))
	}

	// Depth limit: traversal stops when depth reaches maxDepth, so maxDepth=1
	// yields just the root (pinned by the DependencyTree conformance case).
	tree2, _ := s.GetDependencyTree(ctx, "t-3", 1, false, false)
	if len(tree2) != 1 {
		t.Errorf("depth-1 tree len = %d, want 1 (root only)", len(tree2))
	}
}
