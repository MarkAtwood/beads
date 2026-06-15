package flatfile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// AddIssueComment creates a new comment file under comments/{issueID}/.
func (s *FlatFileStore) AddIssueComment(_ context.Context, issueID, author, text string) (*types.Comment, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	// Verify issue exists.
	if _, err := s.readIssue(issueID); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	comment := &types.Comment{
		ID:        fmt.Sprintf("%d", now.UnixNano()),
		IssueID:   issueID,
		Author:    author,
		Text:      text,
		CreatedAt: now,
	}

	dir := filepath.Join(s.commentsDir, issueID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("flatfile: create comments dir: %w", err)
	}

	filename := now.Format("2006-01-02T15-04-05.000000000Z") + ".json"
	path := filepath.Join(dir, filename)

	data, err := json.MarshalIndent(comment, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("flatfile: marshal comment: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, fmt.Errorf("flatfile: write comment tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("flatfile: rename comment: %w", err)
	}

	return comment, nil
}

// GetIssueComments returns all comments for an issue, sorted by created_at.
func (s *FlatFileStore) GetIssueComments(_ context.Context, issueID string) ([]*types.Comment, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	dir := filepath.Join(s.commentsDir, issueID)
	entries, err := readDirSafe(dir)
	if err != nil {
		return nil, fmt.Errorf("flatfile: read comments dir: %w", err)
	}

	var comments []*types.Comment
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("flatfile: read comment %s: %w", e.Name(), err)
		}
		var c types.Comment
		if err := json.Unmarshal(data, &c); err != nil {
			return nil, fmt.Errorf("flatfile: unmarshal comment %s: %w", e.Name(), err)
		}
		comments = append(comments, &c)
	}

	sort.Slice(comments, func(i, j int) bool {
		return comments[i].CreatedAt.Before(comments[j].CreatedAt)
	})

	return comments, nil
}

// CountIssueComments returns the number of comments for an issue.
func (s *FlatFileStore) CountIssueComments(_ context.Context, issueID string) (int64, error) {
	if err := s.checkClosed(); err != nil {
		return 0, err
	}

	count, err := s.countCommentFiles(issueID)
	if err != nil {
		return 0, err
	}
	return int64(count), nil
}
