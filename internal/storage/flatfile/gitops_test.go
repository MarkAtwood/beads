package flatfile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRun runs git in dir and fails the test on error.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitTry(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// gitTry runs git in dir and returns combined output and the error.
func gitTry(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// newGitStore creates a git repo on the given branch with an initial commit
// containing one issue file under .beads/issues/, and returns a store rooted
// in it plus the repo dir.
func newGitStore(t *testing.T, branch string) (*FlatFileStore, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", branch)
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "test")
	gitRun(t, dir, "config", "commit.gpgsign", "false")

	issuesDir := filepath.Join(dir, ".beads", "issues")
	if err := os.MkdirAll(issuesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeIssueFile(t, dir, "test-1", "initial")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")

	s, err := NewFlatFileStore(filepath.Join(dir, ".beads"))
	if err != nil {
		t.Fatalf("NewFlatFileStore: %v", err)
	}
	return s, dir
}

// writeIssueFile writes a minimal issue JSON with the given title.
func writeIssueFile(t *testing.T, repoDir, id, title string) {
	t.Helper()
	path := filepath.Join(repoDir, ".beads", "issues", id+".json")
	content := `{"id":"` + id + `","title":"` + title + `","status":"open","priority":2,"issue_type":"task"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeConflictedMerge drives the repo into a merge with a conflict on the
// test-1 issue file. Returns the name of the side branch.
func makeConflictedMerge(t *testing.T, dir, branch string) {
	t.Helper()
	gitRun(t, dir, "checkout", "-q", "-b", "side")
	writeIssueFile(t, dir, "test-1", "side edit")
	gitRun(t, dir, "commit", "-q", "-am", "side edit")
	gitRun(t, dir, "checkout", "-q", branch)
	writeIssueFile(t, dir, "test-1", "local edit")
	gitRun(t, dir, "commit", "-q", "-am", "local edit")
	if _, err := gitTry(dir, "merge", "side"); err == nil {
		t.Fatal("expected merge conflict, merge succeeded")
	}
	if out := gitRun(t, dir, "diff", "--name-only", "--diff-filter=U"); out == "" {
		t.Fatal("expected unmerged paths after conflicted merge")
	}
}

// TASKS-rkqg: CommitPending must commit only .beads/, never sweep the user's
// own staged files into a bd auto-commit.
func TestCommitPendingScopedToBeads(t *testing.T) {
	ctx := context.Background()
	s, dir := newGitStore(t, "main")

	// User stages their own file; bd has a pending issue change.
	if err := os.WriteFile(filepath.Join(dir, "app.c"), []byte("int main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "app.c")
	writeIssueFile(t, dir, "test-1", "pending edit")

	committed, err := s.CommitPending(ctx, "")
	if err != nil {
		t.Fatalf("CommitPending: %v", err)
	}
	if !committed {
		t.Fatal("expected CommitPending to commit the pending .beads change")
	}
	// Oracle: git — the auto-commit contains only .beads paths, and app.c is
	// still staged for the user's own commit.
	files := gitRun(t, dir, "show", "--name-only", "--format=", "HEAD")
	if strings.Contains(files, "app.c") {
		t.Fatalf("auto-commit swept user file: %q", files)
	}
	if !strings.Contains(files, ".beads/issues/test-1.json") {
		t.Fatalf("auto-commit missing bd change: %q", files)
	}
	if staged := gitRun(t, dir, "diff", "--cached", "--name-only"); !strings.Contains(staged, "app.c") {
		t.Fatalf("user's staged file lost from index: %q", staged)
	}
}

// newPeerClone creates a bare peer repo seeded from repoDir (registered there
// as remote "peer") plus a working clone of it, and returns the clone dir.
func newPeerClone(t *testing.T, repoDir, branch string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "peer.git")
	gitRun(t, filepath.Dir(bare), "init", "-q", "--bare", "-b", branch, bare)
	gitRun(t, repoDir, "remote", "add", "peer", bare)
	gitRun(t, repoDir, "push", "-q", "peer", branch)

	clone := filepath.Join(t.TempDir(), "clone")
	gitRun(t, filepath.Dir(clone), "clone", "-q", bare, clone)
	gitRun(t, clone, "config", "user.email", "peer@example.com")
	gitRun(t, clone, "config", "user.name", "peer")
	gitRun(t, clone, "config", "commit.gpgsign", "false")
	return clone
}

// TASKS-vl2x: Push/PushTo must not stage .beads/ as a side effect — push
// publishes commits, and a stealth 'git add' pollutes the user's index.
func TestPushDoesNotTouchIndex(t *testing.T) {
	ctx := context.Background()
	s, dir := newGitStore(t, "main")
	newPeerClone(t, dir, "main")
	gitRun(t, dir, "branch", "--set-upstream-to=peer/main", "main")

	// Uncommitted bd change sitting in the worktree.
	writeIssueFile(t, dir, "test-1", "uncommitted edit")

	if err := s.Push(ctx); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if staged := gitRun(t, dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("Push staged files as a side effect: %q", staged)
	}
	if err := s.PushTo(ctx, "peer"); err != nil {
		t.Fatalf("PushTo: %v", err)
	}
	if err := s.ForcePush(ctx); err != nil {
		t.Fatalf("ForcePush: %v", err)
	}
	if staged := gitRun(t, dir, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("push staged files as a side effect: %q", staged)
	}
}

// TASKS-7u7b: a conflicted Sync must leave the unmerged index entries intact
// (no "git add" sweep) so ResolveConflicts still has stage-2/3 entries to work
// with, and no conflict-markered JSON is ever staged as resolved.
func TestSyncConflictPreservesUnmergedPaths(t *testing.T) {
	ctx := context.Background()
	s, dir := newGitStore(t, "main")
	clone := newPeerClone(t, dir, "main")

	// Diverge: peer edits test-1 one way, local another.
	writeIssueFile(t, clone, "test-1", "peer edit")
	gitRun(t, clone, "commit", "-q", "-am", "peer edit")
	gitRun(t, clone, "push", "-q", "origin", "main")
	writeIssueFile(t, dir, "test-1", "local edit")
	gitRun(t, dir, "commit", "-q", "-am", "local edit")

	res, err := s.Sync(ctx, "peer", "")
	if err == nil {
		t.Fatal("expected Sync to fail with merge conflict")
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].IssueID != "test-1" {
		t.Fatalf("expected conflict on test-1, got %+v", res.Conflicts)
	}
	// Oracle: git's own unmerged-path listing. If Sync had staged .beads/,
	// this would be empty and resolution would silently no-op.
	if out := gitRun(t, dir, "diff", "--name-only", "--diff-filter=U"); !strings.Contains(out, "test-1.json") {
		t.Fatalf("unmerged entry for test-1.json lost after Sync; diff-filter=U = %q", out)
	}
	if err := s.ResolveConflicts(ctx, "", "ours"); err != nil {
		t.Fatalf("ResolveConflicts: %v", err)
	}
	if err := s.CommitMergeResolution(ctx, "resolve"); err != nil {
		t.Fatalf("CommitMergeResolution: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".beads", "issues", "test-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "local edit") || strings.Contains(string(data), "<<<<<<<") {
		t.Fatalf("ours-resolution did not produce local content: %q", string(data))
	}
}

// TASKS-vun8: a pathspec-limited commit is forbidden during a merge; the
// resolution commit must be a plain commit that concludes the merge
// (oracle: git itself — MERGE_HEAD must be gone afterwards).
func TestCommitMergeResolutionConcludesMerge(t *testing.T) {
	ctx := context.Background()
	s, dir := newGitStore(t, "main")
	makeConflictedMerge(t, dir, "main")

	if err := s.ResolveConflicts(ctx, "", "theirs"); err != nil {
		t.Fatalf("ResolveConflicts: %v", err)
	}
	if err := s.CommitMergeResolution(ctx, "resolve"); err != nil {
		t.Fatalf("CommitMergeResolution: %v", err)
	}
	if _, err := gitTry(dir, "rev-parse", "-q", "--verify", "MERGE_HEAD"); err == nil {
		t.Fatal("MERGE_HEAD still present: merge was not concluded")
	}
	data, err := os.ReadFile(filepath.Join(dir, ".beads", "issues", "test-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "<<<<<<<") {
		t.Fatal("conflict markers persisted in issue file")
	}
}
