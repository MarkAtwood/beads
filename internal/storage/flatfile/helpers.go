package flatfile

import (
	"errors"
	"io/fs"
	"os"
)

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
