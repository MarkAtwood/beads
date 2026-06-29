package flatfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/idgen"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// issueFilename returns the filesystem path for an issue ID.
// Callers in hot paths (loadAllIssues) that already validated the ID
// may use the raw filepath.Join; all other callers go through readIssue/writeIssue
// which validate via safePath.
func (s *FlatFileStore) issueFilename(id string) string {
	return filepath.Join(s.issuesDir, id+".json")
}

// safeIssueFilename returns the path after validating the ID is safe.
func (s *FlatFileStore) safeIssueFilename(id string) (string, error) {
	return safePath(s.issuesDir, id, ".json")
}

// generateID creates a hash-based issue ID, checking the filesystem for collisions.
// Mirrors the algorithm in issueops.GenerateIssueIDInTable.
func (s *FlatFileStore) generateID(prefix string, issue *types.Issue, actor string) (string, error) {
	const maxLength = 8
	baseLength := 4

	for length := baseLength; length <= maxLength; length++ {
		for nonce := 0; nonce < 10; nonce++ {
			candidate := idgen.GenerateHashID(prefix, issue.Title, issue.Description, actor, issue.CreatedAt, length, nonce)
			path := s.issueFilename(candidate)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("flatfile: failed to generate unique ID after trying lengths %d-%d", baseLength, maxLength)
}

// readIssue reads and unmarshals a single issue file.
func (s *FlatFileStore) readIssue(id string) (*types.Issue, error) {
	path, err := s.safeIssueFilename(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, storage.ErrNotFound
		}
		return nil, fmt.Errorf("flatfile: read issue %s: %w", id, err)
	}

	var issue types.Issue
	if err := json.Unmarshal(data, &issue); err != nil {
		// Attempt to repair non-RFC3339 timestamps (e.g. MySQL datetime
		// "2026-06-22 14:31:53" instead of "2026-06-22T14:31:53Z").
		if repaired := repairMySQLTimestamps(data); repaired != nil {
			if err2 := json.Unmarshal(repaired, &issue); err2 == nil {
				return &issue, nil
			}
		}
		return nil, fmt.Errorf("flatfile: unmarshal issue %s: %w", id, err)
	}
	return &issue, nil
}

// mysqlDatetimeRe matches MySQL datetime strings inside JSON values.
var mysqlDatetimeRe = regexp.MustCompile(`"(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2}:\d{2})"`)

// repairMySQLTimestamps rewrites MySQL datetime strings to RFC3339 in raw JSON.
// Returns nil if no replacements were made.
func repairMySQLTimestamps(data []byte) []byte {
	if !mysqlDatetimeRe.Match(data) {
		return nil
	}
	return mysqlDatetimeRe.ReplaceAll(data, []byte(`"${1}T${2}Z"`))
}

// writeIssue marshals and writes an issue to disk atomically.
func (s *FlatFileStore) writeIssue(issue *types.Issue) error {
	path, err := s.safeIssueFilename(issue.ID)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(issue, "", "  ")
	if err != nil {
		return fmt.Errorf("flatfile: marshal issue %s: %w", issue.ID, err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("flatfile: write issue tmp %s: %w", issue.ID, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("flatfile: rename issue %s: %w", issue.ID, err)
	}
	return nil
}

// loadAllIssues reads and unmarshals every issue file in the issues directory.
func (s *FlatFileStore) loadAllIssues() ([]*types.Issue, error) {
	entries, err := os.ReadDir(s.issuesDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("flatfile: readdir issues: %w", err)
	}

	var issues []*types.Issue
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".tmp") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		issue, err := s.readIssue(id)
		if err != nil {
			return nil, err
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// CreateIssue writes a new issue to disk. If issue.ID is empty, a hash-based
// ID is generated from the issue prefix, title, description, and timestamp.
func (s *FlatFileStore) CreateIssue(ctx context.Context, issue *types.Issue, actor string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	// Generate ID if not provided.
	if issue.ID == "" {
		prefix, _ := s.GetConfig(ctx, "issue_prefix")
		if prefix == "" {
			return fmt.Errorf("flatfile: cannot generate ID: issue_prefix not configured")
		}
		if issue.PrefixOverride != "" {
			prefix = issue.PrefixOverride
		} else if issue.IDPrefix != "" {
			prefix = prefix + "-" + issue.IDPrefix
		}
		if issue.CreatedAt.IsZero() {
			issue.CreatedAt = time.Now().UTC()
		}
		id, err := s.generateID(prefix, issue, actor)
		if err != nil {
			return err
		}
		issue.ID = id
	}

	// Check for existing issue with same ID.
	if _, err := os.Stat(s.issueFilename(issue.ID)); err == nil {
		return fmt.Errorf("flatfile: issue %s already exists", issue.ID)
	}

	now := time.Now().UTC()
	if issue.CreatedAt.IsZero() {
		issue.CreatedAt = now
	}
	if issue.UpdatedAt.IsZero() {
		issue.UpdatedAt = now
	}
	if issue.CreatedBy == "" {
		issue.CreatedBy = actor
	}
	if issue.Status == "" {
		issue.Status = types.StatusOpen
	}

	return s.writeIssue(issue)
}

// CreateIssues creates multiple issues in sequence.
func (s *FlatFileStore) CreateIssues(ctx context.Context, issues []*types.Issue, actor string) error {
	for _, issue := range issues {
		if err := s.CreateIssue(ctx, issue, actor); err != nil {
			return err
		}
	}
	return nil
}

// GetIssue returns a single issue by ID.
func (s *FlatFileStore) GetIssue(_ context.Context, id string) (*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}
	return s.readIssue(id)
}

// GetIssueByExternalRef scans all issues for a matching external_ref field.
func (s *FlatFileStore) GetIssueByExternalRef(_ context.Context, externalRef string) (*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	issues, err := s.loadAllIssues()
	if err != nil {
		return nil, err
	}
	for _, issue := range issues {
		if issue.ExternalRef != nil && *issue.ExternalRef == externalRef {
			return issue, nil
		}
	}
	return nil, storage.ErrNotFound
}

// GetIssuesByIDs returns issues for the given IDs. Missing IDs are silently skipped.
func (s *FlatFileStore) GetIssuesByIDs(_ context.Context, ids []string) ([]*types.Issue, error) {
	if err := s.checkClosed(); err != nil {
		return nil, err
	}

	var result []*types.Issue
	for _, id := range ids {
		issue, err := s.readIssue(id)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				continue
			}
			return nil, err
		}
		result = append(result, issue)
	}
	return result, nil
}

// UpdateIssue applies a map of field updates to an existing issue.
func (s *FlatFileStore) UpdateIssue(_ context.Context, id string, updates map[string]interface{}, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	issue, err := s.readIssue(id)
	if err != nil {
		return err
	}

	applyUpdates(issue, updates)
	issue.UpdatedAt = time.Now().UTC()

	return s.writeIssue(issue)
}

// ReopenIssue sets the issue status back to open.
func (s *FlatFileStore) ReopenIssue(_ context.Context, id string, reason string, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	issue, err := s.readIssue(id)
	if err != nil {
		return err
	}

	issue.Status = types.StatusOpen
	issue.ClosedAt = nil
	issue.CloseReason = reason
	issue.ClosedBySession = ""
	issue.UpdatedAt = time.Now().UTC()

	return s.writeIssue(issue)
}

// UpdateIssueType changes the issue_type field.
func (s *FlatFileStore) UpdateIssueType(_ context.Context, id string, issueType string, _ string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	issue, err := s.readIssue(id)
	if err != nil {
		return err
	}

	issue.IssueType = types.IssueType(issueType)
	issue.UpdatedAt = time.Now().UTC()

	return s.writeIssue(issue)
}

// CloseIssue marks an issue as closed.
func (s *FlatFileStore) CloseIssue(_ context.Context, id string, reason string, actor string, session string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	issue, err := s.readIssue(id)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	issue.Status = types.StatusClosed
	issue.ClosedAt = &now
	issue.CloseReason = reason
	issue.ClosedBySession = session
	issue.UpdatedAt = now
	if issue.Assignee == "" && actor != "" {
		issue.Assignee = actor
	}

	return s.writeIssue(issue)
}

// DeleteIssue removes an issue file from disk.
func (s *FlatFileStore) DeleteIssue(_ context.Context, id string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}

	path, err := s.safeIssueFilename(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return storage.ErrNotFound
		}
		return fmt.Errorf("flatfile: delete issue %s: %w", id, err)
	}
	return nil
}

// applyUpdates applies a map of field name → value to an issue struct.
// Supported keys match the JSON field names of types.Issue.
func applyUpdates(issue *types.Issue, updates map[string]interface{}) {
	for key, val := range updates {
		switch key {
		case "title":
			if v, ok := val.(string); ok {
				issue.Title = v
			}
		case "description":
			if v, ok := val.(string); ok {
				issue.Description = v
			}
		case "status":
			if v, ok := val.(string); ok {
				issue.Status = types.Status(v)
			}
		case "priority":
			switch v := val.(type) {
			case int:
				issue.Priority = v
			case float64:
				issue.Priority = int(v)
			}
		case "issue_type":
			if v, ok := val.(string); ok {
				issue.IssueType = types.IssueType(v)
			}
		case "assignee":
			if v, ok := val.(string); ok {
				issue.Assignee = v
			}
		case "owner":
			if v, ok := val.(string); ok {
				issue.Owner = v
			}
		case "design":
			if v, ok := val.(string); ok {
				issue.Design = v
			}
		case "acceptance_criteria":
			if v, ok := val.(string); ok {
				issue.AcceptanceCriteria = v
			}
		case "notes":
			if v, ok := val.(string); ok {
				issue.Notes = v
			}
		case "spec_id":
			if v, ok := val.(string); ok {
				issue.SpecID = v
			}
		case "pinned":
			if v, ok := val.(bool); ok {
				issue.Pinned = v
			}
		case "ephemeral":
			if v, ok := val.(bool); ok {
				issue.Ephemeral = v
			}
		case "external_ref":
			if v, ok := val.(string); ok {
				issue.ExternalRef = &v
			}
		case "metadata":
			if v, ok := val.(json.RawMessage); ok {
				issue.Metadata = v
			} else if v, ok := val.(string); ok {
				issue.Metadata = json.RawMessage(v)
			}
		case "due_at":
			if v, ok := val.(*time.Time); ok {
				issue.DueAt = v
			}
		case "defer_until":
			if v, ok := val.(*time.Time); ok {
				issue.DeferUntil = v
			}
		case "estimated_minutes":
			switch v := val.(type) {
			case int:
				issue.EstimatedMinutes = &v
			case float64:
				i := int(v)
				issue.EstimatedMinutes = &i
			case *int:
				issue.EstimatedMinutes = v
			}
		}
	}
}
