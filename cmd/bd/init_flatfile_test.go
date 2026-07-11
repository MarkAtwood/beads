package main

import (
	"os"
	"path/filepath"
	"strings"
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

// TASKS-vs5y: flatfile prefixes become filename components
// (issues/<prefix>-<n>.json); a prefix with a path separator or space must be
// rejected up front — before any side effects — instead of producing a
// workspace where every 'bd create' fails with ENOENT. Auto-detect-shaped
// prefixes (underscores, uppercase) stay accepted for parity with the Dolt
// path's database-name rule.
func TestRunInitFlatfileValidatesPrefix(t *testing.T) {
	for _, tc := range []struct {
		prefix  string
		wantErr bool
	}{
		{"a/b", true},
		{`a\b`, true},
		{"a b", true},
		{"-lead", true},
		{"my_Project", false},
		{"tasks-2", false},
	} {
		t.Run(tc.prefix, func(t *testing.T) {
			origStore := store
			t.Cleanup(func() {
				if store != nil && store != origStore {
					_ = store.Close()
				}
				store = origStore
			})

			tmpDir := t.TempDir()
			t.Chdir(tmpDir)
			beadsDir := filepath.Join(tmpDir, ".beads")

			err := runInitFlatfile(t.Context(), beadsDir, tc.prefix, true, true, true, false)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("runInitFlatfile(prefix=%q) succeeded; want validation error", tc.prefix)
				}
				// Validation must fire before side effects: no half-created workspace.
				if _, statErr := os.Stat(beadsDir); !os.IsNotExist(statErr) {
					t.Errorf("invalid prefix %q left a partially created %s (stat err=%v)", tc.prefix, beadsDir, statErr)
				}
			} else if err != nil {
				t.Fatalf("runInitFlatfile(prefix=%q) failed: %v", tc.prefix, err)
			}
		})
	}
}

// TASKS-vqtt: Dolt-only init flags must be rejected on --backend=flatfile,
// not silently ignored (the user asked for server mode / a named database
// and would silently get neither).
//
// NOTE: this test must run AFTER any in-process test that inits with an
// explicit non-Dolt --backend (tests run in source order within a file):
// setting a flag via Execute adds it to pflag's `actual` set permanently —
// there is no API to remove it — so the allowlist's Flags().Visit sees the
// Dolt-only flags from this test in every later in-process
// `init --backend=<non-dolt>` call, even though their values are reset.
func TestInitFlatfileRejectsDoltOnlyFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag string
		args []string
	}{
		{"server", "--server", []string{"--server"}},
		{"database", "--database", []string{"--database", "mydb"}},
		{"shared-server", "--shared-server", []string{"--shared-server"}},
		{"remote", "--remote", []string{"--remote", "https://example.com/x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flagNames := []string{"backend", "quiet"}
			for _, a := range tc.args {
				if len(a) > 2 && a[:2] == "--" {
					flagNames = append(flagNames, a[2:])
				}
			}
			resetInitFlags(t, flagNames...)

			_, err := runInitCommand(t, append([]string{"--backend=flatfile", "--quiet"}, tc.args...)...)
			if err == nil {
				t.Fatalf("bd init --backend=flatfile %s succeeded; want rejection", tc.flag)
			}
			if !strings.Contains(err.Error(), tc.flag) {
				t.Errorf("error %q does not name the rejected flag %s", err, tc.flag)
			}
		})
	}
}
