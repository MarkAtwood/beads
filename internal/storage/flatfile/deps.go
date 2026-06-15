package flatfile

import (
	"context"
	"fmt"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// AddDependency adds a dependency record to the depending issue's JSON.
func (s *FlatFileStore) AddDependency(_ context.Context, dep *types.Dependency, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	// Validate both issues exist.
	issue, err := s.readIssue(dep.IssueID)
	if err != nil {
		return fmt.Errorf("flatfile: source issue: %w", err)
	}
	if _, err := s.readIssue(dep.DependsOnID); err != nil {
		return fmt.Errorf("flatfile: target issue: %w", err)
	}

	// Check for duplicate.
	for _, existing := range issue.Dependencies {
		if existing.DependsOnID == dep.DependsOnID && existing.Type == dep.Type {
			return nil // idempotent
		}
	}

	if dep.CreatedAt.IsZero() {
		dep.CreatedAt = time.Now().UTC()
	}

	issue.Dependencies = append(issue.Dependencies, dep)
	issue.UpdatedAt = time.Now().UTC()

	return s.writeIssue(issue)
}

// RemoveDependency removes a dependency from the depending issue's JSON.
func (s *FlatFileStore) RemoveDependency(_ context.Context, issueID, dependsOnID string, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return err
	}

	filtered := make([]*types.Dependency, 0, len(issue.Dependencies))
	found := false
	for _, dep := range issue.Dependencies {
		if dep.DependsOnID == dependsOnID && dep.Type != "parent-child" {
			found = true
			continue
		}
		filtered = append(filtered, dep)
	}
	if !found {
		return storage.ErrNotFound
	}

	issue.Dependencies = filtered
	issue.UpdatedAt = time.Now().UTC()

	return s.writeIssue(issue)
}

// GetDependencies returns the issues that issueID depends on.
func (s *FlatFileStore) GetDependencies(_ context.Context, issueID string) ([]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return nil, err
	}

	var result []*types.Issue
	for _, dep := range issue.Dependencies {
		if dep.Type == "parent-child" {
			continue
		}
		target, err := s.readIssue(dep.DependsOnID)
		if err != nil {
			continue // skip missing
		}
		result = append(result, target)
	}
	return result, nil
}

// GetDependents returns issues that depend on issueID (reverse lookup).
func (s *FlatFileStore) GetDependents(_ context.Context, issueID string) ([]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	var result []*types.Issue
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.DependsOnID == issueID && dep.Type != "parent-child" {
				result = append(result, issue)
				break
			}
		}
	}
	return result, nil
}

// GetDependenciesWithMetadata returns dependencies with relationship type info.
func (s *FlatFileStore) GetDependenciesWithMetadata(_ context.Context, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return nil, err
	}

	var result []*types.IssueWithDependencyMetadata
	for _, dep := range issue.Dependencies {
		target, err := s.readIssue(dep.DependsOnID)
		if err != nil {
			continue
		}
		result = append(result, &types.IssueWithDependencyMetadata{
			Issue:          *target,
			DependencyType: dep.Type,
		})
	}
	return result, nil
}

// GetDependentsWithMetadata returns dependents with relationship type info.
func (s *FlatFileStore) GetDependentsWithMetadata(_ context.Context, issueID string) ([]*types.IssueWithDependencyMetadata, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	var result []*types.IssueWithDependencyMetadata
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.DependsOnID == issueID {
				result = append(result, &types.IssueWithDependencyMetadata{
					Issue:          *issue,
					DependencyType: dep.Type,
				})
				break
			}
		}
	}
	return result, nil
}

// GetDependencyTree builds a dependency tree starting from issueID.
func (s *FlatFileStore) GetDependencyTree(_ context.Context, issueID string, maxDepth int, _ bool, reverse bool) ([]*types.TreeNode, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}

	issueMap := make(map[string]*types.Issue, len(all))
	for _, issue := range all {
		issueMap[issue.ID] = issue
	}

	// Build adjacency: forward (deps) or reverse (dependents).
	adj := make(map[string][]string)
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type == "parent-child" {
				continue
			}
			if reverse {
				adj[dep.DependsOnID] = append(adj[dep.DependsOnID], issue.ID)
			} else {
				adj[issue.ID] = append(adj[issue.ID], dep.DependsOnID)
			}
		}
	}

	var result []*types.TreeNode
	visited := make(map[string]bool)

	var walk func(id string, depth int)
	walk = func(id string, depth int) {
		if depth > maxDepth || visited[id] {
			return
		}
		visited[id] = true

		issue := issueMap[id]
		if issue == nil {
			return
		}

		node := &types.TreeNode{
			Issue: *issue,
			Depth: depth,
		}
		result = append(result, node)

		for _, next := range adj[id] {
			walk(next, depth+1)
		}
	}

	walk(issueID, 0)
	return result, nil
}

// CountDependencies returns the number of non-parent-child dependencies.
func (s *FlatFileStore) CountDependencies(_ context.Context, issueID string) (int64, error) {
	if err := s.checkClosed(); err != nil {
		return 0, err
	}

	issue, err := s.readIssue(issueID)
	if err != nil {
		return 0, err
	}

	var count int64
	for _, dep := range issue.Dependencies {
		if dep.Type != "parent-child" {
			count++
		}
	}
	return count, nil
}

// CountDependents returns the number of issues that depend on issueID.
func (s *FlatFileStore) CountDependents(_ context.Context, issueID string) (int64, error) {
	if err := s.checkClosed(); err != nil {
		return 0, err
	}

	all, err := s.loadAllIssues()
	if err != nil {
		return 0, err
	}

	var count int64
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.DependsOnID == issueID && dep.Type != "parent-child" {
				count++
				break
			}
		}
	}
	return count, nil
}
