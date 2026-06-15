package flatfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// SetConfig sets a key-value pair in the config store.
func (s *FlatFileStore) SetConfig(_ context.Context, key, value string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	return s.setKV(s.configKVPath, key, value)
}

// GetConfig retrieves a value from the config store.
func (s *FlatFileStore) GetConfig(_ context.Context, key string) (string, error) {
	if err := s.checkClosed(); err != nil {
		return "", err
	}
	return s.getKV(s.configKVPath, key)
}

// GetAllConfig returns all config key-value pairs.
func (s *FlatFileStore) GetAllConfig(_ context.Context) (map[string]string, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	return s.readKVFile(s.configKVPath)
}

// SetLocalMetadata sets a key-value in the local (non-git-tracked) metadata store.
func (s *FlatFileStore) SetLocalMetadata(_ context.Context, key, value string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	return s.setKV(s.localMetaPath, key, value)
}

// GetLocalMetadata retrieves a value from the local metadata store.
// Returns ("", nil) if the key does not exist.
func (s *FlatFileStore) GetLocalMetadata(_ context.Context, key string) (string, error) {
	if err := s.checkClosed(); err != nil {
		return "", err
	}
	return s.getKV(s.localMetaPath, key)
}

// readKVFile reads a JSON key-value file.
func (s *FlatFileStore) readKVFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return make(map[string]string), nil
		}
		return nil, fmt.Errorf("flatfile: read kv %s: %w", path, err)
	}

	var kv map[string]string
	if err := json.Unmarshal(data, &kv); err != nil {
		return nil, fmt.Errorf("flatfile: parse kv %s: %w", path, err)
	}
	if kv == nil {
		kv = make(map[string]string)
	}
	return kv, nil
}

// writeKVFile writes a JSON key-value file atomically.
func (s *FlatFileStore) writeKVFile(path string, kv map[string]string) error {
	data, err := json.MarshalIndent(kv, "", "  ")
	if err != nil {
		return fmt.Errorf("flatfile: marshal kv: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("flatfile: write kv tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("flatfile: rename kv: %w", err)
	}
	return nil
}

func (s *FlatFileStore) setKV(path, key, value string) error {
	s.kvMu.Lock()
	defer s.kvMu.Unlock()

	kv, err := s.readKVFile(path)
	if err != nil {
		return err
	}
	kv[key] = value
	return s.writeKVFile(path, kv)
}

func (s *FlatFileStore) getKV(path, key string) (string, error) {
	kv, err := s.readKVFile(path)
	if err != nil {
		return "", err
	}
	return kv[key], nil
}
