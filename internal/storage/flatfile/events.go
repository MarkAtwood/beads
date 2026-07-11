package flatfile

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// eventFilename returns the JSONL events file path for an issue.
func (s *FlatFileStore) eventFilename(issueID string) string {
	return filepath.Join(s.eventsDir, issueID+".jsonl")
}

// appendEvent appends a single event to the issue's JSONL events file,
// generating an ID and timestamp when absent. This is the flat-file
// counterpart of the events/wisp_events INSERTs in issueops.
func (s *FlatFileStore) appendEvent(ev *types.Event) error {
	if ev.ID == "" {
		ev.ID = issueops.NewEventID()
	}
	if ev.CreatedAt.IsZero() {
		ev.CreatedAt = time.Now().UTC()
	}
	path, err := s.safeEventFilename(ev.IssueID)
	if err != nil {
		return err
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("flatfile: marshal event: %w", err)
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("flatfile: open events for append: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("flatfile: append event: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("flatfile: sync events: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("flatfile: close events: %w", err)
	}
	return nil
}

// recordEvent mirrors issueops.RecordEventInTable/RecordFullEventInTable:
// old_value and new_value are written explicitly (possibly empty, never NULL).
func (s *FlatFileStore) recordEvent(issueID string, eventType types.EventType, actor, oldValue, newValue string) error {
	return s.appendEvent(&types.Event{
		IssueID:   issueID,
		EventType: eventType,
		Actor:     actor,
		OldValue:  &oldValue,
		NewValue:  &newValue,
	})
}

// recordCommentEvent mirrors the label/comment event INSERTs, which leave
// old_value/new_value unwritten: NULL on durable issues (events table has no
// DEFAULT) but empty-string on wisps (wisp_events columns default to the
// empty string).
func (s *FlatFileStore) recordCommentEvent(issueID string, isWisp bool, eventType types.EventType, actor, comment string) error {
	ev := &types.Event{
		IssueID:   issueID,
		EventType: eventType,
		Actor:     actor,
		Comment:   &comment,
	}
	if isWisp {
		empty := ""
		ev.OldValue = &empty
		ev.NewValue = &empty
	}
	return s.appendEvent(ev)
}

// safeEventFilename validates the issue ID before constructing the path.
func (s *FlatFileStore) safeEventFilename(issueID string) (string, error) {
	return safePath(s.eventsDir, issueID, ".jsonl")
}

// GetEvents returns audit events for an issue, newest first, capped at limit.
func (s *FlatFileStore) GetEvents(_ context.Context, issueID string, limit int) ([]*types.Event, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	events, err := s.readEventsFile(issueID)
	if err != nil {
		return nil, err
	}

	// Sort newest first.
	sort.Slice(events, func(i, j int) bool {
		return events[i].CreatedAt.After(events[j].CreatedAt)
	})

	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}
	return events, nil
}

// GetAllEventsSince returns all events across all issues since the given time.
func (s *FlatFileStore) GetAllEventsSince(_ context.Context, since time.Time) ([]*types.Event, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	entries, err := readDirSafe(s.eventsDir)
	if err != nil {
		return nil, fmt.Errorf("flatfile: readdir events: %w", err)
	}

	var result []*types.Event
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		issueID := strings.TrimSuffix(e.Name(), ".jsonl")
		events, err := s.readEventsFile(issueID)
		if err != nil {
			return nil, err
		}
		for _, ev := range events {
			// Strict boundary, mirroring issueops' `created_at > ?`.
			if ev.CreatedAt.After(since) {
				result = append(result, ev)
			}
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

// CountEvents returns the count of events for an issue.
func (s *FlatFileStore) CountEvents(_ context.Context, issueID string, limit int) (int64, error) {
	if err := s.checkClosed(); err != nil {
		return 0, err
	}

	events, err := s.readEventsFile(issueID)
	if err != nil {
		return 0, err
	}

	count := int64(len(events))
	if limit > 0 && count > int64(limit) {
		count = int64(limit)
	}
	return count, nil
}

// readEventsFile reads all events from a per-issue JSONL file.
func (s *FlatFileStore) readEventsFile(issueID string) ([]*types.Event, error) {
	path, err := s.safeEventFilename(issueID)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("flatfile: open events: %w", err)
	}
	defer f.Close()

	var events []*types.Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev types.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("flatfile: unmarshal event: %w", err)
		}
		events = append(events, &ev)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("flatfile: scan events: %w", err)
	}
	return events, nil
}
