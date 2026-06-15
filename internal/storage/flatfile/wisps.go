package flatfile

import (
	"context"

	"github.com/steveyegge/beads/internal/types"
)

// ListWisps returns ephemeral issues matching the filter.
func (s *FlatFileStore) ListWisps(_ context.Context, filter types.WispFilter) ([]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	var result []*types.Issue
	for _, issue := range all {
		if !issue.Ephemeral {
			continue
		}
		if !matchesWispFilter(issue, filter) {
			continue
		}
		result = append(result, issue)
		if filter.Limit > 0 && len(result) >= filter.Limit {
			break
		}
	}
	return result, nil
}

func matchesWispFilter(issue *types.Issue, f types.WispFilter) bool {
	if f.Type != nil && issue.IssueType != *f.Type {
		return false
	}
	if f.Status != nil {
		if issue.Status != *f.Status {
			return false
		}
	} else if !f.IncludeClosed {
		// Default: non-closed only
		if issue.Status == types.StatusClosed {
			return false
		}
	}
	if f.UpdatedAfter != nil && !issue.UpdatedAt.After(*f.UpdatedAfter) {
		return false
	}
	if f.UpdatedBefore != nil && !issue.UpdatedAt.Before(*f.UpdatedBefore) {
		return false
	}
	return true
}
