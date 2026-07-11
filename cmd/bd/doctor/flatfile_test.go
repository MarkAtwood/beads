package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TASKS-v479: git does not track empty directories, so a fresh clone of a
// flatfile repo is missing comments/, events/, memories/ (and issues/ when
// no issues exist yet). NewFlatFileStore auto-creates them on the next store
// open, so doctor must not report the workspace as broken.
func TestCheckFlatfileDirsFreshCloneIsHealthy(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"backend":"flatfile","project_id":"test"}` + "\n"
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}

	check := checkFlatfileDirs(beadsDir)
	if check.Status != StatusOK {
		t.Errorf("fresh clone without empty subdirs: got status %q (message %q), want %q — "+
			"missing dirs are auto-created on next store open and must not fail doctor",
			check.Status, check.Message, StatusOK)
	}
}

// TASKS-4gnf: outside any git repo there is nothing for flat-file sync to
// work through — the Git Tracking check must not report the false all-clear
// "tracked by git" (git check-ignore exits non-zero both for "not ignored"
// and "not a repo").
func TestCheckFlatfileGitTrackingNoRepoIsNotOK(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "issues"), 0o755); err != nil {
		t.Fatal(err)
	}

	check := checkFlatfileGitTracking(beadsDir)
	if check.Status == StatusOK {
		t.Errorf("no git repo: got StatusOK (%q); want a warning that there is no repo to sync through", check.Message)
	}

	// After git init, an un-ignored issues dir is a clean pass again.
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	check = checkFlatfileGitTracking(beadsDir)
	if check.Status != StatusOK {
		t.Errorf("git repo, not ignored: got status %q (%q); want %q", check.Status, check.Message, StatusOK)
	}
}

// All dirs present stays OK.
func TestCheckFlatfileDirsAllPresent(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	for _, d := range []string{"issues", "comments", "events", "memories"} {
		if err := os.MkdirAll(filepath.Join(beadsDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	check := checkFlatfileDirs(beadsDir)
	if check.Status != StatusOK {
		t.Errorf("all dirs present: got status %q, want %q", check.Status, StatusOK)
	}
}
