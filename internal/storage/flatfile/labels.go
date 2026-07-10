package flatfile

import (
	"context"
	"sort"
	"time"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// AddLabel adds a label to an issue. Mirrors issueops.AddLabelInTx: the label
// row is idempotent (INSERT IGNORE) but the label_added event is recorded
// unconditionally.
func (s *FlatFileStore) AddLabel(_ context.Context, issueID, label, actor string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return err
	}

	present := false
	for _, l := range issue.Labels {
		if l == label {
			present = true
			break
		}
	}
	if !present {
		issue.Labels = append(issue.Labels, label)
		issue.UpdatedAt = time.Now().UTC()
		if err := s.writeIssue(issue); err != nil {
			return err
		}
	}
	return s.recordCommentEvent(issueID, issueops.IsWisp(issue), types.EventLabelAdded, actor, "Added label: "+label)
}

// RemoveLabel removes a label from an issue. Mirrors issueops.RemoveLabelInTx:
// removing an absent label is not an error, but the label_removed event is
// recorded unconditionally.
func (s *FlatFileStore) RemoveLabel(_ context.Context, issueID, label, actor string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return err
	}

	filtered := make([]string, 0, len(issue.Labels))
	removed := false
	for _, l := range issue.Labels {
		if l == label {
			removed = true
			continue
		}
		filtered = append(filtered, l)
	}
	if removed {
		issue.Labels = filtered
		issue.UpdatedAt = time.Now().UTC()
		if err := s.writeIssue(issue); err != nil {
			return err
		}
	}
	return s.recordCommentEvent(issueID, issueops.IsWisp(issue), types.EventLabelRemoved, actor, "Removed label: "+label)
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
	// Mirror issueops' bare `ORDER BY label`: binary/code-point ordering.
	labels := append([]string(nil), issue.Labels...)
	sort.Strings(labels)
	return labels, nil
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
