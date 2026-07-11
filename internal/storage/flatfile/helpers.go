package flatfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// validateID rejects issue IDs that could escape the target directory via path
// traversal. IDs must not contain path separators or ".." components.
func validateID(id string) error {
	if id == "" {
		return fmt.Errorf("flatfile: empty issue ID")
	}
	if strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("flatfile: invalid issue ID %q: contains path separator", id)
	}
	if id == ".." || strings.HasPrefix(id, ".."+string(filepath.Separator)) || strings.HasSuffix(id, string(filepath.Separator)+"..") || strings.Contains(id, string(filepath.Separator)+".."+string(filepath.Separator)) {
		return fmt.Errorf("flatfile: invalid issue ID %q: contains path traversal", id)
	}
	// Belt and suspenders: the "." prefix check catches ".." and hidden files.
	if strings.HasPrefix(id, ".") {
		return fmt.Errorf("flatfile: invalid issue ID %q: must not start with '.'", id)
	}
	return nil
}

// safePath constructs a path under parentDir and verifies the result doesn't escape.
func safePath(parentDir, child, ext string) (string, error) {
	if err := validateID(child); err != nil {
		return "", err
	}
	result := filepath.Join(parentDir, child+ext)
	// Verify the resolved path is actually under parentDir.
	abs, err := filepath.Abs(result)
	if err != nil {
		return "", fmt.Errorf("flatfile: resolve path: %w", err)
	}
	absParent, err := filepath.Abs(parentDir)
	if err != nil {
		return "", fmt.Errorf("flatfile: resolve parent: %w", err)
	}
	if !strings.HasPrefix(abs, absParent+string(filepath.Separator)) && abs != absParent {
		return "", fmt.Errorf("flatfile: path %q escapes directory %q", result, parentDir)
	}
	return result, nil
}

// atomicWriteFile writes data to path durably and atomically: a uniquely-named
// temp file in the same directory is written, fsynced, closed, chmodded to
// 0644, then renamed over path. The unique temp name (os.CreateTemp) keeps two
// concurrent writers of the same path from renaming each other's half-written
// temp into place; the fsync keeps a crash right after the rename from
// persisting an empty or truncated file. The directory is deliberately not
// fsynced: losing the rename itself just resurfaces the previous consistent
// version, which is acceptable here and avoids doubling the fsync cost.
func atomicWriteFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readDirSafe reads a directory, returning nil (not error) if it doesn't exist.
func readDirSafe(dir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}
