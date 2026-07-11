// Package flatfile provides a flat-file storage backend for beads.
//
// Each issue is stored as a separate JSON file under .beads/issues/,
// with comments in .beads/comments/ and config in .beads/config_kv.json.
// No database server is required; git provides version control and sync.
package flatfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// FlatFileStore implements the storage.Storage interface using one JSON file
// per issue. It also satisfies storage.StoreLocator and storage.LifecycleManager.
// The embedded unsupportedDoltStorage shell answers the few methods the backend
// deliberately omits (FederationStore) with typed *storage.ErrUnsupported;
// every method implemented on *FlatFileStore shadows it.
type FlatFileStore struct {
	unsupportedDoltStorage

	beadsDir      string // root .beads/ directory
	issuesDir     string // .beads/issues/
	commentsDir   string // .beads/comments/
	memoriesDir   string // .beads/memories/
	eventsDir     string // .beads/events/
	counterPath   string // .beads/counter.json
	configKVPath  string // .beads/config_kv.json
	localMetaPath string // .beads/local_metadata.json
	mergeslotPath string // .beads/merge_slot.json
	repoMtimePath string // .beads/repo_mtime.json

	// Normalized custom-status/type stores, the flat-file analog of the
	// custom_statuses/custom_types tables. Synced by SetConfig and — like
	// the SQL tables — deliberately left untouched by DeleteConfig.
	customStatusesPath string // .beads/custom_statuses.json
	customTypesPath    string // .beads/custom_types.json

	mu         sync.RWMutex // protects counter file
	kvMu       sync.Mutex   // protects config/metadata KV read-modify-write
	commentSeq atomic.Int64 // monotonic counter for comment IDs

	// writeMu serializes every read-modify-write cycle on issue files and the
	// merge-slot file: the whole read → mutate → writeIssue span of a mutation
	// runs under it, so concurrent same-issue mutations in one process cannot
	// lose updates (the SQL backends get this from transactions). It is a
	// single store-wide mutex, not a per-issue striped lock, on purpose: bd is
	// a single-process CLI where fsync dominates write cost, and multi-issue
	// mutations (UpdateIssueID inbound-edge rewrite, DeleteIssues cascade,
	// batch create, ReclaimExpiredLeases) would need ordered multi-lock
	// acquisition under striping. Pure readers do not take it — writeIssue's
	// atomic rename already guarantees they see a consistent file. Holders
	// must not call another writeMu-taking method (the lock is not reentrant);
	// wrappers like ReopenIssue/UpdateIssueType/ClaimReadyIssue therefore rely
	// on their inner call for locking. Cross-process mutual exclusion is out
	// of scope: the flat-file store assumes one writing process per checkout.
	writeMu sync.Mutex

	closed atomic.Bool
}

// NewFlatFileStore creates a FlatFileStore rooted at beadsDir (typically ".beads").
// It creates the required subdirectories if they do not exist.
func NewFlatFileStore(beadsDir string) (*FlatFileStore, error) {
	s, err := newFlatFileStoreNoCreate(beadsDir)
	if err != nil {
		return nil, err
	}

	dirs := []string{s.issuesDir, s.commentsDir, s.memoriesDir, s.eventsDir}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("flatfile: create directory %s: %w", d, err)
		}
	}

	return s, nil
}

// newFlatFileStoreNoCreate builds a FlatFileStore without touching the
// filesystem. Read paths tolerate the missing subdirectories (readDirSafe,
// loadAllIssues); read-only cross-repo opens use this so they never mutate a
// foreign checkout (GH#3231).
func newFlatFileStoreNoCreate(beadsDir string) (*FlatFileStore, error) {
	abs, err := filepath.Abs(beadsDir)
	if err != nil {
		return nil, fmt.Errorf("flatfile: resolve beads dir: %w", err)
	}

	s := &FlatFileStore{
		beadsDir:      abs,
		issuesDir:     filepath.Join(abs, "issues"),
		commentsDir:   filepath.Join(abs, "comments"),
		memoriesDir:   filepath.Join(abs, "memories"),
		eventsDir:     filepath.Join(abs, "events"),
		counterPath:   filepath.Join(abs, "counter.json"),
		configKVPath:  filepath.Join(abs, "config_kv.json"),
		localMetaPath: filepath.Join(abs, "local_metadata.json"),
		mergeslotPath: filepath.Join(abs, "merge_slot.json"),
		repoMtimePath: filepath.Join(abs, "repo_mtime.json"),

		customStatusesPath: filepath.Join(abs, "custom_statuses.json"),
		customTypesPath:    filepath.Join(abs, "custom_types.json"),
	}

	return s, nil
}

// Close marks the store as closed. Subsequent operations should return an error.
func (s *FlatFileStore) Close() error {
	s.closed.Store(true)
	return nil
}

// IsClosed reports whether Close has been called.
func (s *FlatFileStore) IsClosed() bool {
	return s.closed.Load()
}

// Path returns the root .beads/ directory path.
func (s *FlatFileStore) Path() string {
	return s.beadsDir
}

// CLIDir returns the root .beads/ directory path (same as Path for flat-file).
func (s *FlatFileStore) CLIDir() string {
	return s.beadsDir
}

// errClosed is returned by operations called after Close.
var errClosed = fmt.Errorf("flatfile: store is closed")

// checkClosed returns errClosed if the store has been closed.
func (s *FlatFileStore) checkClosed() error {
	if s.closed.Load() {
		return errClosed
	}
	return nil
}
