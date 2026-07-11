package doctor

import (
	"os"
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
