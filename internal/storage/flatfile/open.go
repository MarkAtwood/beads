package flatfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/steveyegge/beads/internal/storage"
)

// IsFlatFileBackend checks metadata.json in beadsDir for backend: "flatfile".
func IsFlatFileBackend(beadsDir string) bool {
	data, err := os.ReadFile(filepath.Join(beadsDir, "metadata.json"))
	if err != nil {
		return false
	}
	var meta struct {
		Backend string `json:"backend"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return false
	}
	return meta.Backend == "flatfile"
}

// OpenStore creates a FlatFileStore from a beads directory.
// Returns the store as storage.DoltStorage for use with HookFiringStore.
func OpenStore(_ context.Context, beadsDir string) (storage.DoltStorage, error) {
	abs, err := filepath.Abs(beadsDir)
	if err != nil {
		return nil, fmt.Errorf("flatfile: resolve path: %w", err)
	}

	// Verify metadata.json exists and says flatfile.
	metaPath := filepath.Join(abs, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("flatfile: metadata.json not found in %s", abs)
		}
		return nil, fmt.Errorf("flatfile: read metadata: %w", err)
	}
	var meta struct {
		Backend string `json:"backend"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("flatfile: parse metadata: %w", err)
	}
	if meta.Backend != "flatfile" {
		return nil, fmt.Errorf("flatfile: metadata.json backend is %q, not \"flatfile\"", meta.Backend)
	}

	return NewFlatFileStore(abs)
}
