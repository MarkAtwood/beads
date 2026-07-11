package flatfile

import (
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// txForTest opens a transaction and hands it to fn, ignoring the commit step.
func txForTest(t *testing.T, s *FlatFileStore, fn func(tx storage.Transaction)) {
	t.Helper()
	err := s.RunInTransaction(ctx, "", func(tx storage.Transaction) error {
		fn(tx)
		return nil
	})
	if err != nil {
		t.Fatalf("RunInTransaction: %v", err)
	}
}

// Non-blocking dependency types must not participate in blocking-cycle
// detection: sqlite/dolt build the graph from blocks/conditional-blocks only
// (issueops.AppendBlockingGraphInTx), so 'A related B' plus a new 'B blocks A'
// is not a cycle on any SQL backend.
func TestCycleThroughEdgesIgnoresNonBlockingDeps(t *testing.T) {
	s := newTestStore(t)

	for _, id := range []string{"test-a", "test-b"} {
		if err := s.CreateIssue(ctx, &types.Issue{ID: id, Title: id}, "t"); err != nil {
			t.Fatalf("CreateIssue(%s): %v", id, err)
		}
	}
	if err := s.AddDependency(ctx, &types.Dependency{
		IssueID: "test-a", DependsOnID: "test-b", Type: types.DepRelated,
	}, "t"); err != nil {
		t.Fatalf("AddDependency(related): %v", err)
	}
	if err := s.AddDependency(ctx, &types.Dependency{
		IssueID: "test-b", DependsOnID: "test-a", Type: types.DepBlocks,
	}, "t"); err != nil {
		t.Fatalf("AddDependency(blocks): %v", err)
	}

	txForTest(t, s, func(tx storage.Transaction) {
		cycle, err := tx.CycleThroughEdges(ctx, [][2]string{{"test-b", "test-a"}})
		if err != nil {
			t.Fatalf("CycleThroughEdges: %v", err)
		}
		if cycle != "" {
			t.Errorf("cycle = %q, want none: related edges must not close blocking cycles", cycle)
		}
	})
}

// A real blocking cycle renders exactly as the shared SQL-backend renderer
// does ("u → v → u", issueops cycles_test.go) — no duplicated first node.
func TestCycleThroughEdgesRendersWithoutDuplicateNode(t *testing.T) {
	s := newTestStore(t)

	// Write the cyclic pair directly; AddDependency's per-edge check would
	// reject the second edge before CycleThroughEdges ever saw the graph.
	a := &types.Issue{ID: "test-a", Title: "a", Dependencies: []*types.Dependency{
		{IssueID: "test-a", DependsOnID: "test-b", Type: types.DepBlocks},
	}}
	b := &types.Issue{ID: "test-b", Title: "b", Dependencies: []*types.Dependency{
		{IssueID: "test-b", DependsOnID: "test-a", Type: types.DepBlocks},
	}}
	for _, issue := range []*types.Issue{a, b} {
		if err := s.writeIssue(issue); err != nil {
			t.Fatalf("writeIssue(%s): %v", issue.ID, err)
		}
	}

	txForTest(t, s, func(tx storage.Transaction) {
		cycle, err := tx.CycleThroughEdges(ctx, [][2]string{{"test-b", "test-a"}})
		if err != nil {
			t.Fatalf("CycleThroughEdges: %v", err)
		}
		if want := "test-b → test-a → test-b"; cycle != want {
			t.Errorf("cycle = %q, want %q", cycle, want)
		}
	})
}

// A self-loop renders as "a → a", matching issueops cycles_test.go.
func TestCycleThroughEdgesSelfLoop(t *testing.T) {
	s := newTestStore(t)
	if err := s.writeIssue(&types.Issue{ID: "test-a", Title: "a", Dependencies: []*types.Dependency{
		{IssueID: "test-a", DependsOnID: "test-a", Type: types.DepBlocks},
	}}); err != nil {
		t.Fatalf("writeIssue: %v", err)
	}

	txForTest(t, s, func(tx storage.Transaction) {
		cycle, err := tx.CycleThroughEdges(ctx, [][2]string{{"test-a", "test-a"}})
		if err != nil {
			t.Fatalf("CycleThroughEdges: %v", err)
		}
		if want := "test-a → test-a"; cycle != want {
			t.Errorf("cycle = %q, want %q", cycle, want)
		}
	})
}
