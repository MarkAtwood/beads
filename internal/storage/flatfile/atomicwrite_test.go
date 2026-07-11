package flatfile

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// TestWriteIssueConcurrentSamePath exercises the unique-temp-name guarantee:
// with a fixed "<path>.tmp" name, concurrent writers of the SAME issue file
// rename each other's half-written temp into place or fail ENOENT. Every
// write must succeed, the surviving file must parse, and no temp files may
// be left behind.
func TestWriteIssueConcurrentSamePath(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateIssue(ctx, &types.Issue{ID: "tmp-race", Title: "base"}, "tester"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			issue := &types.Issue{ID: "tmp-race", Title: fmt.Sprintf("writer %d", idx)}
			if err := s.writeIssue(issue); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("writeIssue: %v", err)
	}

	issue, err := s.readIssue("tmp-race")
	if err != nil {
		t.Fatalf("readIssue after concurrent writes: %v", err)
	}
	if !strings.HasPrefix(issue.Title, "writer ") {
		t.Errorf("title = %q, want a writer's value", issue.Title)
	}

	entries, err := os.ReadDir(s.issuesDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
