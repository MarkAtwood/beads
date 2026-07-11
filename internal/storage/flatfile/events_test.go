package flatfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// SQL reference semantics: an event row of any size is stored and returned
// intact. The flatfile reader must not impose a line-length cap (bufio.Scanner
// defaults to 64KB) on events embedding large issue snapshots.
func TestGetEventsLargeEvent(t *testing.T) {
	s := newTestStore(t)

	issue := &types.Issue{ID: "big-1", Title: "Big"}
	if err := s.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	big := strings.Repeat("x", 200*1024) // well past Scanner's 64KB default
	if err := s.recordEvent("big-1", types.EventUpdated, "tester", big, "new"); err != nil {
		t.Fatalf("recordEvent: %v", err)
	}

	events, err := s.GetEvents(ctx, "big-1", 0)
	if err != nil {
		t.Fatalf("GetEvents after >64KB event: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.OldValue != nil && *ev.OldValue == big {
			found = true
		}
	}
	if !found {
		t.Error("large event not returned intact")
	}

	if _, err := s.GetAllEventsSince(ctx, time.Time{}); err != nil {
		t.Errorf("GetAllEventsSince after >64KB event: %v", err)
	}
	if n, err := s.CountEvents(ctx, "big-1", 0); err != nil || n != int64(len(events)) {
		t.Errorf("CountEvents = %d, %v; want %d, nil", n, err, len(events))
	}
}

// SQL reference semantics: one bad row cannot make every other event row
// unreadable. A torn append (crash mid-write, no trailing newline) must not
// brick GetEvents/GetAllEventsSince, and the next append must not merge into
// the torn fragment.
func TestReadEventsTolerantOfTornLine(t *testing.T) {
	s := newTestStore(t)

	issue := &types.Issue{ID: "torn-1", Title: "Torn"}
	if err := s.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	before, err := s.GetEvents(ctx, "torn-1", 0)
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}

	// Simulate a crash mid-append: partial JSON, no trailing newline.
	path := s.eventFilename("torn-1")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open events file: %v", err)
	}
	if _, err := f.WriteString(`{"id":"torn","issue_id":"torn-1","event_ty`); err != nil {
		t.Fatalf("write torn fragment: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// The next event must survive the torn fragment.
	if err := s.recordEvent("torn-1", types.EventUpdated, "tester", "old", "new"); err != nil {
		t.Fatalf("recordEvent after torn line: %v", err)
	}

	after, err := s.GetEvents(ctx, "torn-1", 0)
	if err != nil {
		t.Fatalf("GetEvents after torn line: %v", err)
	}
	if want := len(before) + 1; len(after) != want {
		t.Errorf("got %d events, want %d (torn fragment skipped, new event kept)", len(after), want)
	}
	if _, err := s.GetAllEventsSince(ctx, time.Time{}); err != nil {
		t.Errorf("GetAllEventsSince with torn line: %v", err)
	}
}

// SQL reference semantics: the events query never sees stray filesystem
// artifacts. A dotfile like macOS AppleDouble "._TASKS-1.jsonl" in the events
// dir must not fail the store-wide scan.
func TestGetAllEventsSinceSkipsStrayDotfiles(t *testing.T) {
	s := newTestStore(t)

	issue := &types.Issue{ID: "real-1", Title: "Real"}
	if err := s.CreateIssue(ctx, issue, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	stray := filepath.Join(s.eventsDir, "._real-1.jsonl")
	if err := os.WriteFile(stray, []byte("\x00\x05\x16\x07AppleDouble junk"), 0o644); err != nil {
		t.Fatalf("write stray dotfile: %v", err)
	}

	events, err := s.GetAllEventsSince(ctx, time.Time{})
	if err != nil {
		t.Fatalf("GetAllEventsSince with stray dotfile: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.IssueID == "real-1" && ev.EventType == types.EventCreated {
			found = true
		}
	}
	if !found {
		t.Error("real issue's created event missing from scan")
	}
}
