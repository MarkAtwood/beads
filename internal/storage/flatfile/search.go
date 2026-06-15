package flatfile

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/types"
)

// SearchIssues loads all issues, applies the text query and filter, then returns
// the matching slice. For < 10k issues this is fast (all in memory).
func (s *FlatFileStore) SearchIssues(_ context.Context, query string, filter types.IssueFilter) ([]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	var result []*types.Issue
	for _, issue := range all {
		if matchesQuery(issue, query) && matchesFilter(issue, filter) {
			result = append(result, issue)
		}
	}

	sortIssues(result, filter.SortBy, filter.SortDesc)

	if filter.Offset > 0 && filter.Offset < len(result) {
		result = result[filter.Offset:]
	} else if filter.Offset >= len(result) {
		return nil, nil
	}
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}

	return result, nil
}

// SearchIssuesWithCounts is like SearchIssues but attaches dependency and comment counts.
func (s *FlatFileStore) SearchIssuesWithCounts(ctx context.Context, query string, filter types.IssueFilter) ([]*types.IssueWithCounts, error) {
	issues, err := s.SearchIssues(ctx, query, filter)
	if err != nil {
		return nil, err
	}

	// Build reverse dep index for dependent counts.
	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}
	depCounts := make(map[string]int)
	parentMap := make(map[string]string)
	for _, a := range all {
		for _, dep := range a.Dependencies {
			if dep.Type == "parent-child" {
				parentMap[a.ID] = dep.DependsOnID
			} else {
				depCounts[dep.DependsOnID]++
			}
		}
	}

	var result []*types.IssueWithCounts
	for _, issue := range issues {
		depCount := 0
		for _, dep := range issue.Dependencies {
			if dep.Type != "parent-child" {
				depCount++
			}
		}
		commentCount, _ := s.countCommentFiles(issue.ID)
		wc := &types.IssueWithCounts{
			Issue:           issue,
			DependencyCount: depCount,
			DependentCount:  depCounts[issue.ID],
			CommentCount:    commentCount,
		}
		if p, ok := parentMap[issue.ID]; ok {
			wc.Parent = &p
		}
		result = append(result, wc)
	}
	return result, nil
}

// CountIssues returns the number of issues matching query and filter.
func (s *FlatFileStore) CountIssues(ctx context.Context, query string, filter types.IssueFilter) (int64, error) {
	// Ignore Limit/Offset for count.
	filter.Limit = 0
	filter.Offset = 0
	issues, err := s.SearchIssues(ctx, query, filter)
	if err != nil {
		return 0, err
	}
	return int64(len(issues)), nil
}

// CountIssuesByGroup returns per-group counts for the given groupBy field.
func (s *FlatFileStore) CountIssuesByGroup(ctx context.Context, filter types.IssueFilter, groupBy string) (map[string]int, error) {
	filter.Limit = 0
	filter.Offset = 0
	issues, err := s.SearchIssues(ctx, "", filter)
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int)
	for _, issue := range issues {
		var key string
		switch groupBy {
		case "status":
			key = string(issue.Status)
		case "priority":
			key = fmt.Sprintf("%d", issue.Priority)
		case "type", "issue_type":
			key = string(issue.IssueType)
		case "assignee":
			key = issue.Assignee
			if key == "" {
				key = "(unassigned)"
			}
		case "label":
			if len(issue.Labels) == 0 {
				counts["(none)"]++
			} else {
				for _, l := range issue.Labels {
					counts[l]++
				}
			}
			continue
		default:
			key = "(unknown)"
		}
		counts[key]++
	}
	return counts, nil
}

// countCommentFiles returns the number of comment files for an issue.
func (s *FlatFileStore) countCommentFiles(issueID string) (int, error) {
	dir, err := safePath(s.commentsDir, issueID, "")
	if err != nil {
		return 0, err
	}
	entries, err := readDirSafe(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	return count, nil
}

// matchesQuery returns true if the issue matches a text search query.
func matchesQuery(issue *types.Issue, query string) bool {
	if query == "" {
		return true
	}
	q := strings.ToLower(query)
	return strings.Contains(strings.ToLower(issue.Title), q) ||
		strings.Contains(strings.ToLower(issue.Description), q) ||
		strings.Contains(strings.ToLower(issue.ID), q) ||
		strings.Contains(strings.ToLower(issue.Notes), q)
}

// matchesFilter returns true if the issue matches all filter criteria.
func matchesFilter(issue *types.Issue, f types.IssueFilter) bool {
	// Status filters
	if f.Status != nil && issue.Status != *f.Status {
		return false
	}
	if len(f.Statuses) > 0 {
		found := false
		for _, s := range f.Statuses {
			if issue.Status == s {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	for _, es := range f.ExcludeStatus {
		if issue.Status == es {
			return false
		}
	}

	// Priority
	if f.Priority != nil && issue.Priority != *f.Priority {
		return false
	}
	if f.PriorityMin != nil && issue.Priority < *f.PriorityMin {
		return false
	}
	if f.PriorityMax != nil && issue.Priority > *f.PriorityMax {
		return false
	}

	// Type
	if f.IssueType != nil && issue.IssueType != *f.IssueType {
		return false
	}
	for _, et := range f.ExcludeTypes {
		if issue.IssueType == et {
			return false
		}
	}

	// Assignee
	if f.Assignee != nil && issue.Assignee != *f.Assignee {
		return false
	}
	if f.NoAssignee && issue.Assignee != "" {
		return false
	}

	// Labels (AND semantics)
	if len(f.Labels) > 0 {
		labelSet := makeStringSet(issue.Labels)
		for _, l := range f.Labels {
			if !labelSet[l] {
				return false
			}
		}
	}
	// Labels (OR semantics)
	if len(f.LabelsAny) > 0 {
		labelSet := makeStringSet(issue.Labels)
		found := false
		for _, l := range f.LabelsAny {
			if labelSet[l] {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	// Label exclusion
	if len(f.ExcludeLabels) > 0 {
		labelSet := makeStringSet(issue.Labels)
		for _, l := range f.ExcludeLabels {
			if labelSet[l] {
				return false
			}
		}
	}
	// Label pattern (glob)
	if f.LabelPattern != "" {
		found := false
		for _, l := range issue.Labels {
			if matched, _ := filepath.Match(f.LabelPattern, l); matched {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	// Label regex
	if f.LabelRegex != "" {
		re, err := regexp.Compile(f.LabelRegex)
		if err == nil {
			found := false
			for _, l := range issue.Labels {
				if re.MatchString(l) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	if f.NoLabels && len(issue.Labels) > 0 {
		return false
	}

	// ID filters
	if len(f.IDs) > 0 {
		idSet := makeStringSet(f.IDs)
		if !idSet[issue.ID] {
			return false
		}
	}
	if f.IDPrefix != "" && !strings.HasPrefix(issue.ID, f.IDPrefix) {
		return false
	}
	if f.SpecIDPrefix != "" && !strings.HasPrefix(issue.SpecID, f.SpecIDPrefix) {
		return false
	}

	// Text contains
	if f.TitleContains != "" && !containsCI(issue.Title, f.TitleContains) {
		return false
	}
	if f.TitleSearch != "" && !containsCI(issue.Title, f.TitleSearch) {
		return false
	}
	if f.DescriptionContains != "" && !containsCI(issue.Description, f.DescriptionContains) {
		return false
	}
	if f.NotesContains != "" && !containsCI(issue.Notes, f.NotesContains) {
		return false
	}
	if f.ExternalRefContains != "" {
		ref := ""
		if issue.ExternalRef != nil {
			ref = *issue.ExternalRef
		}
		if !containsCI(ref, f.ExternalRefContains) {
			return false
		}
	}

	// Empty checks
	if f.EmptyDescription && issue.Description != "" {
		return false
	}

	// Date ranges
	if f.CreatedAfter != nil && !issue.CreatedAt.After(*f.CreatedAfter) {
		return false
	}
	if f.CreatedBefore != nil && !issue.CreatedAt.Before(*f.CreatedBefore) {
		return false
	}
	if f.UpdatedAfter != nil && !issue.UpdatedAt.After(*f.UpdatedAfter) {
		return false
	}
	if f.UpdatedBefore != nil && !issue.UpdatedAt.Before(*f.UpdatedBefore) {
		return false
	}
	if f.ClosedAfter != nil {
		if issue.ClosedAt == nil || !issue.ClosedAt.After(*f.ClosedAfter) {
			return false
		}
	}
	if f.ClosedBefore != nil {
		if issue.ClosedAt == nil || !issue.ClosedAt.Before(*f.ClosedBefore) {
			return false
		}
	}
	if f.StartedAfter != nil {
		if issue.StartedAt == nil || !issue.StartedAt.After(*f.StartedAfter) {
			return false
		}
	}
	if f.StartedBefore != nil {
		if issue.StartedAt == nil || !issue.StartedAt.Before(*f.StartedBefore) {
			return false
		}
	}

	// Source repo
	if f.SourceRepo != nil && issue.SourceRepo != *f.SourceRepo {
		return false
	}

	// Ephemeral
	if f.Ephemeral != nil {
		if *f.Ephemeral && !issue.Ephemeral {
			return false
		}
		if !*f.Ephemeral && issue.Ephemeral {
			return false
		}
	}

	// Pinned
	if f.Pinned != nil {
		if *f.Pinned && !issue.Pinned {
			return false
		}
		if !*f.Pinned && issue.Pinned {
			return false
		}
	}

	// Template
	if f.IsTemplate != nil {
		if *f.IsTemplate && !issue.IsTemplate {
			return false
		}
		if !*f.IsTemplate && issue.IsTemplate {
			return false
		}
	}

	// MolType
	if f.MolType != nil && issue.MolType != *f.MolType {
		return false
	}

	// WispType
	if f.WispType != nil && issue.WispType != *f.WispType {
		return false
	}

	// Deferred
	if f.Deferred {
		isDeferred := issue.DeferUntil != nil || issue.Status == types.StatusDeferred
		if !isDeferred {
			return false
		}
	}
	if f.DeferAfter != nil {
		if issue.DeferUntil == nil || !issue.DeferUntil.After(*f.DeferAfter) {
			return false
		}
	}
	if f.DeferBefore != nil {
		if issue.DeferUntil == nil || !issue.DeferUntil.Before(*f.DeferBefore) {
			return false
		}
	}
	if f.DueAfter != nil {
		if issue.DueAt == nil || !issue.DueAt.After(*f.DueAfter) {
			return false
		}
	}
	if f.DueBefore != nil {
		if issue.DueAt == nil || !issue.DueAt.Before(*f.DueBefore) {
			return false
		}
	}
	if f.Overdue {
		now := time.Now()
		if issue.DueAt == nil || !issue.DueAt.Before(now) || issue.Status == types.StatusClosed {
			return false
		}
	}

	// Metadata
	if len(f.MetadataFields) > 0 && len(issue.Metadata) > 0 {
		var meta map[string]interface{}
		if json.Unmarshal(issue.Metadata, &meta) == nil {
			for k, v := range f.MetadataFields {
				mv, ok := meta[k]
				if !ok {
					return false
				}
				if fmt.Sprintf("%v", mv) != v {
					return false
				}
			}
		} else {
			return false
		}
	} else if len(f.MetadataFields) > 0 {
		return false
	}
	if f.HasMetadataKey != "" {
		if len(issue.Metadata) == 0 {
			return false
		}
		var meta map[string]interface{}
		if json.Unmarshal(issue.Metadata, &meta) != nil {
			return false
		}
		if _, ok := meta[f.HasMetadataKey]; !ok {
			return false
		}
	}

	// Parent filtering — requires scanning deps for parent-child type.
	// ParentID and NoParent are handled via the dependencies array.
	if f.ParentID != nil {
		found := false
		for _, dep := range issue.Dependencies {
			if dep.Type == "parent-child" && dep.DependsOnID == *f.ParentID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if f.NoParent {
		for _, dep := range issue.Dependencies {
			if dep.Type == "parent-child" {
				return false
			}
		}
	}

	return true
}

func sortIssues(issues []*types.Issue, sortBy string, desc bool) {
	if sortBy == "" {
		sortBy = "created_at"
	}

	sort.Slice(issues, func(i, j int) bool {
		var less bool
		switch sortBy {
		case "priority":
			less = issues[i].Priority < issues[j].Priority
		case "updated_at":
			less = issues[i].UpdatedAt.Before(issues[j].UpdatedAt)
		case "title":
			less = issues[i].Title < issues[j].Title
		case "status":
			less = issues[i].Status < issues[j].Status
		default: // created_at
			less = issues[i].CreatedAt.Before(issues[j].CreatedAt)
		}
		if desc {
			return !less
		}
		return less
	})
}

func containsCI(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func makeStringSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
