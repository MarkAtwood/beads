package flatfile

import (
	"context"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestGetReadyWorkNoDeps(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "r-1", Title: "No deps", Status: types.StatusOpen}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "r-2", Title: "Closed", Status: types.StatusClosed}, "tester")

	ready, err := s.GetReadyWork(ctx, types.WorkFilter{})
	if err != nil {
		t.Fatalf("GetReadyWork: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "r-1" {
		t.Errorf("ready = %v, want [r-1]", ready)
	}
}

func TestGetReadyWorkWithClosedDep(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "w-1", Title: "Dep", Status: types.StatusClosed}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "w-2", Title: "Depends on closed", Status: types.StatusOpen}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "w-2", DependsOnID: "w-1", Type: "blocks"}, "tester")

	ready, err := s.GetReadyWork(ctx, types.WorkFilter{})
	if err != nil {
		t.Fatalf("GetReadyWork: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "w-2" {
		t.Errorf("ready = %v, want [w-2]", ready)
	}
}

func TestGetReadyWorkBlockedByOpenDep(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "b-1", Title: "Blocker", Status: types.StatusOpen}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "b-2", Title: "Blocked", Status: types.StatusOpen}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "b-2", DependsOnID: "b-1", Type: "blocks"}, "tester")

	ready, err := s.GetReadyWork(ctx, types.WorkFilter{})
	if err != nil {
		t.Fatalf("GetReadyWork: %v", err)
	}
	// Only b-1 should be ready (b-2 is blocked)
	if len(ready) != 1 || ready[0].ID != "b-1" {
		ids := make([]string, len(ready))
		for i, r := range ready {
			ids[i] = r.ID
		}
		t.Errorf("ready IDs = %v, want [b-1]", ids)
	}
}

func TestGetBlockedIssues(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "bl-1", Title: "Open dep", Status: types.StatusOpen}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "bl-2", Title: "Blocked issue", Status: types.StatusOpen}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "bl-2", DependsOnID: "bl-1", Type: "blocks"}, "tester")

	blocked, err := s.GetBlockedIssues(ctx, types.WorkFilter{})
	if err != nil {
		t.Fatalf("GetBlockedIssues: %v", err)
	}
	if len(blocked) != 1 {
		t.Fatalf("blocked len = %d, want 1", len(blocked))
	}
	if blocked[0].ID != "bl-2" {
		t.Errorf("blocked ID = %s, want bl-2", blocked[0].ID)
	}
	if blocked[0].BlockedByCount != 1 {
		t.Errorf("BlockedByCount = %d, want 1", blocked[0].BlockedByCount)
	}
}

func TestGetEpicsEligibleForClosure(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "e-1", Title: "Epic", IssueType: "epic", Status: types.StatusOpen}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "e-1.1", Title: "Child 1", Status: types.StatusClosed}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "e-1.2", Title: "Child 2", Status: types.StatusClosed}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "e-1.1", DependsOnID: "e-1", Type: "parent-child"}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "e-1.2", DependsOnID: "e-1", Type: "parent-child"}, "tester")

	epics, err := s.GetEpicsEligibleForClosure(ctx)
	if err != nil {
		t.Fatalf("GetEpicsEligibleForClosure: %v", err)
	}
	if len(epics) != 1 {
		t.Fatalf("len = %d, want 1", len(epics))
	}
	if !epics[0].EligibleForClose {
		t.Error("epic should be eligible for closure")
	}
	if epics[0].TotalChildren != 2 {
		t.Errorf("TotalChildren = %d, want 2", epics[0].TotalChildren)
	}
}

func TestGetEpicsNotEligibleOpenChild(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "f-1", Title: "Epic", IssueType: "epic", Status: types.StatusOpen}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "f-1.1", Title: "Closed child", Status: types.StatusClosed}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "f-1.2", Title: "Open child", Status: types.StatusOpen}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "f-1.1", DependsOnID: "f-1", Type: "parent-child"}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "f-1.2", DependsOnID: "f-1", Type: "parent-child"}, "tester")

	epics, _ := s.GetEpicsEligibleForClosure(ctx)
	if len(epics) != 1 {
		t.Fatalf("len = %d, want 1", len(epics))
	}
	if epics[0].EligibleForClose {
		t.Error("epic should NOT be eligible with open child")
	}
}
