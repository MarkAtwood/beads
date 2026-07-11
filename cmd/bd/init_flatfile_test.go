package main

import (
	"os"
	"path/filepath"
	"testing"
)

// resetInitFlags restores initCmd flag state so one test's flags do not leak
// into the next Execute call (cobra flag values persist per process).
func resetInitFlags(t *testing.T, names ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, name := range names {
			f := initCmd.Flags().Lookup(name)
			if f == nil {
				t.Fatalf("unknown init flag %q", name)
			}
			_ = f.Value.Set(f.DefValue)
			f.Changed = false
		}
	})
}

// runInitCommand executes "bd init" with the given extra args in a fresh temp
// dir and returns the workspace's .beads dir and the Execute error.
func runInitCommand(t *testing.T, extraArgs ...string) (string, error) {
	t.Helper()

	origDBPath := dbPath
	origStore := store
	t.Cleanup(func() {
		if store != nil && store != origStore {
			_ = store.Close()
		}
		store = origStore
		dbPath = origDBPath
	})
	dbPath = ""
	store = nil

	// Neutralize Dolt-mode environment leakage: a leftover shared-server or
	// server-mode env var must not influence flatfile init (TASKS-9tsg).
	t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	t.Setenv("BEADS_DIR", "")
	os.Unsetenv("BEADS_DIR")
	os.Unsetenv("BEADS_DOLT_SHARED_SERVER")
	os.Unsetenv("BEADS_DOLT_SERVER_MODE")

	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	rootCmd.SetArgs(append([]string{"init"}, extraArgs...))
	err := rootCmd.Execute()
	return filepath.Join(tmpDir, ".beads"), err
}

// TASKS-9tsg: flatfile init must dispatch before the embedded-Dolt lock and
// shared-server startup — a flat-file workspace must never contain the
// Dolt-only .beads/embeddeddolt/ directory (its presence makes clones be
// misclassified as Dolt workspaces).
func TestInitFlatfileCreatesNoEmbeddedDoltArtifacts(t *testing.T) {
	resetInitFlags(t, "backend", "prefix", "quiet", "skip-hooks", "skip-agents")

	beadsDir, err := runInitCommand(t,
		"--backend=flatfile", "--prefix", "tst", "--quiet", "--skip-hooks", "--skip-agents")
	if err != nil {
		t.Fatalf("bd init --backend=flatfile failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(beadsDir, "issues")); err != nil {
		t.Errorf("expected flatfile issues/ dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(beadsDir, "metadata.json")); err != nil {
		t.Errorf("expected metadata.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(beadsDir, "embeddeddolt")); !os.IsNotExist(err) {
		t.Errorf("flatfile init created Dolt artifact .beads/embeddeddolt (stat err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(beadsDir, "dolt")); !os.IsNotExist(err) {
		t.Errorf("flatfile init created Dolt artifact .beads/dolt (stat err=%v)", err)
	}
}
