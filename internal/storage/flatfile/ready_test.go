package flatfile

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// SQL reference: BuildReadyWorkWhere routes MetadataFields/HasMetadataKey
// through sqlbuild.AppendMetadataClauses (JSON_EXTRACT equality / non-NULL),
// so 'bd ready --metadata-field k=v' and '--has-metadata-key k' narrow the
// ready set on dolt/sqlite. Flatfile must match.
func TestGetReadyWorkMetadataFilters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "md-1", Title: "Core", Status: types.StatusOpen,
		Metadata: json.RawMessage(`{"team":"core","shard":7}`)}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "md-2", Title: "Infra", Status: types.StatusOpen,
		Metadata: json.RawMessage(`{"team":"infra"}`)}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "md-3", Title: "No metadata", Status: types.StatusOpen}, "tester")

	ready, err := s.GetReadyWork(ctx, types.WorkFilter{MetadataFields: map[string]string{"team": "core"}})
	if err != nil {
		t.Fatalf("GetReadyWork metadata-field: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "md-1" {
		t.Errorf("metadata-field team=core: got %v, want [md-1]", readyIDs(ready))
	}

	ready, err = s.GetReadyWork(ctx, types.WorkFilter{HasMetadataKey: "shard"})
	if err != nil {
		t.Fatalf("GetReadyWork has-metadata-key: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "md-1" {
		t.Errorf("has-metadata-key shard: got %v, want [md-1]", readyIDs(ready))
	}

	// No metadata filter still returns everything.
	ready, err = s.GetReadyWork(ctx, types.WorkFilter{})
	if err != nil {
		t.Fatalf("GetReadyWork unfiltered: %v", err)
	}
	if len(ready) != 3 {
		t.Errorf("unfiltered: got %v, want 3 issues", readyIDs(ready))
	}
}

// SQL reference: the sqlbuild ready-work parent clause ORs the transitive
// descendant set with "id LIKE 'parent.%' AND id NOT IN (parent-child
// issue_ids)", so a dotted-ID child with no dependency row is still returned.
func TestGetReadyWorkParentImplicitDottedChild(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "pr-1", Title: "Parent", Status: types.StatusOpen}, "tester")
	// Explicit child via parent-child dep.
	s.CreateIssue(ctx, &types.Issue{ID: "pr-1.1", Title: "Explicit child", Status: types.StatusOpen}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "pr-1.1", DependsOnID: "pr-1", Type: types.DepParentChild}, "tester")
	// Implicit child: dotted ID, no dep row.
	s.CreateIssue(ctx, &types.Issue{ID: "pr-1.2", Title: "Implicit child", Status: types.StatusOpen}, "tester")
	// Dotted ID but explicitly parented elsewhere: excluded by the SQL clause.
	s.CreateIssue(ctx, &types.Issue{ID: "pr-1.3", Title: "Parented elsewhere", Status: types.StatusOpen}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "pr-9", Title: "Other parent", Status: types.StatusOpen}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "pr-1.3", DependsOnID: "pr-9", Type: types.DepParentChild}, "tester")

	parent := "pr-1"
	ready, err := s.GetReadyWork(ctx, types.WorkFilter{ParentID: &parent})
	if err != nil {
		t.Fatalf("GetReadyWork: %v", err)
	}
	got := make(map[string]bool)
	for _, r := range ready {
		got[r.ID] = true
	}
	if len(ready) != 2 || !got["pr-1.1"] || !got["pr-1.2"] {
		t.Errorf("ready = %v, want [pr-1.1 pr-1.2]", readyIDs(ready))
	}
}

// SQL reference: issueops.GetBlockedIssuesInTx matches direct parent-child
// children OR strings.HasPrefix(id, parentID+".") for --parent filtering.
func TestGetBlockedIssuesParentImplicitDottedChild(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	s.CreateIssue(ctx, &types.Issue{ID: "bp-blk", Title: "Blocker", Status: types.StatusOpen}, "tester")
	// Dotted-ID child of bp-1 with no parent-child dep, blocked by bp-blk.
	s.CreateIssue(ctx, &types.Issue{ID: "bp-1.1", Title: "Implicit blocked child", Status: types.StatusOpen}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "bp-1.1", DependsOnID: "bp-blk", Type: types.DepBlocks}, "tester")
	// Blocked issue outside the parent scope.
	s.CreateIssue(ctx, &types.Issue{ID: "bp-other", Title: "Other blocked", Status: types.StatusOpen}, "tester")
	s.AddDependency(ctx, &types.Dependency{IssueID: "bp-other", DependsOnID: "bp-blk", Type: types.DepBlocks}, "tester")

	parent := "bp-1"
	blocked, err := s.GetBlockedIssues(ctx, types.WorkFilter{ParentID: &parent})
	if err != nil {
		t.Fatalf("GetBlockedIssues: %v", err)
	}
	if len(blocked) != 1 || blocked[0].ID != "bp-1.1" {
		ids := make([]string, len(blocked))
		for i, b := range blocked {
			ids[i] = b.ID
		}
		t.Errorf("blocked = %v, want [bp-1.1]", ids)
	}
}

func readyIDs(issues []*types.Issue) []string {
	ids := make([]string, len(issues))
	for i, r := range issues {
		ids[i] = r.ID
	}
	return ids
}

// SQL reference (sqlbuild.BuildReadyWorkWhere): an explicit Status filter is
// applied verbatim, and the deferred clause is only
// (defer_until IS NULL OR defer_until <= now). A status=deferred issue with
// no future defer_until IS returned by Status=deferred on dolt/sqlite.
func TestGetReadyWorkExplicitDeferredStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	future := time.Now().Add(24 * time.Hour)
	s.CreateIssue(ctx, &types.Issue{ID: "def-1", Title: "Deferred no date", Status: types.StatusDeferred}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "def-2", Title: "Deferred future", Status: types.StatusDeferred, DeferUntil: &future}, "tester")
	s.CreateIssue(ctx, &types.Issue{ID: "def-3", Title: "Open", Status: types.StatusOpen}, "tester")

	ready, err := s.GetReadyWork(ctx, types.WorkFilter{Status: types.StatusDeferred})
	if err != nil {
		t.Fatalf("GetReadyWork: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "def-1" {
		ids := make([]string, len(ready))
		for i, r := range ready {
			ids[i] = r.ID
		}
		t.Errorf("ready IDs = %v, want [def-1]", ids)
	}

	// Default filter still excludes deferred-status issues (status IN
	// (open, in_progress)) — def-3 only.
	ready, err = s.GetReadyWork(ctx, types.WorkFilter{})
	if err != nil {
		t.Fatalf("GetReadyWork default: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "def-3" {
		t.Errorf("default ready = %v, want [def-3]", ready)
	}
}

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
