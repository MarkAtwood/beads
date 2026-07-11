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
