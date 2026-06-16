package flatfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// counterState holds the per-prefix sequential ID counters.
type counterState struct {
	Prefixes map[string]int `json:"prefixes"`
}

// nextSequentialID returns the next sequential ID for the given prefix and
// increments the counter atomically. The caller must not hold s.mu.
func (s *FlatFileStore) nextSequentialID(prefix string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readCounter()
	if err != nil {
		return "", err
	}

	next := state.Prefixes[prefix] + 1
	state.Prefixes[prefix] = next

	if err := s.writeCounter(state); err != nil {
		return "", err
	}

	return fmt.Sprintf("%s-%d", prefix, next), nil
}

// peekSequentialID returns the last allocated sequential ID for a prefix
// without incrementing. Returns 0 if no IDs have been allocated.
func (s *FlatFileStore) peekSequentialID(prefix string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	state, err := s.readCounter()
	if err != nil {
		return 0, err
	}

	return state.Prefixes[prefix], nil
}

// readCounter reads the counter state from disk.
// Caller must hold s.mu (read or write).
func (s *FlatFileStore) readCounter() (*counterState, error) {
	data, err := os.ReadFile(s.counterPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &counterState{Prefixes: make(map[string]int)}, nil
		}
		return nil, fmt.Errorf("flatfile: read counter: %w", err)
	}

	var state counterState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("flatfile: parse counter: %w", err)
	}
	if state.Prefixes == nil {
		state.Prefixes = make(map[string]int)
	}
	return &state, nil
}

// writeCounter writes the counter state to disk atomically (write-then-rename).
// Caller must hold s.mu for writing.
func (s *FlatFileStore) writeCounter(state *counterState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("flatfile: marshal counter: %w", err)
	}
	data = append(data, '\n')

	tmp := s.counterPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("flatfile: write counter tmp: %w", err)
	}
	if err := os.Rename(tmp, s.counterPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("flatfile: rename counter: %w", err)
	}
	return nil
}
