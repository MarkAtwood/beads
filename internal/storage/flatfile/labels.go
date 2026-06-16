package flatfile

import (
	"context"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// AddLabel adds a label to an issue (idempotent).
func (s *FlatFileStore) AddLabel(_ context.Context, issueID, label, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return err
	}

	for _, l := range issue.Labels {
		if l == label {
			return nil // already present
		}
	}

	issue.Labels = append(issue.Labels, label)
	issue.UpdatedAt = time.Now().UTC()

	return s.writeIssue(issue)
}

// RemoveLabel removes a label from an issue.
func (s *FlatFileStore) RemoveLabel(_ context.Context, issueID, label, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return err
	}

	filtered := make([]string, 0, len(issue.Labels))
	for _, l := range issue.Labels {
		if l != label {
			filtered = append(filtered, l)
		}
	}

	issue.Labels = filtered
	issue.UpdatedAt = time.Now().UTC()

	return s.writeIssue(issue)
}

// GetLabels returns the labels for an issue.
func (s *FlatFileStore) GetLabels(_ context.Context, issueID string) ([]string, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return nil, err
	}

	if issue.Labels == nil {
		return []string{}, nil
	}
	return issue.Labels, nil
}

// GetIssuesByLabel returns all issues that have the given label.
func (s *FlatFileStore) GetIssuesByLabel(_ context.Context, label string) ([]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	var result []*types.Issue
	for _, issue := range all {
		for _, l := range issue.Labels {
			if l == label {
				result = append(result, issue)
				break
			}
		}
	}
	return result, nil
}
