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
	"github.com/steveyegge/beads/internal/storage/issueops"
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

	issue, err := decodeIssue(data)
	if err != nil {
		// Attempt to repair non-RFC3339 timestamps (e.g. MySQL datetime
		// "2026-06-22 14:31:53" instead of "2026-06-22T14:31:53Z").
		if repaired := repairMySQLTimestamps(data); repaired != nil {
			if fixed, err2 := decodeIssue(repaired); err2 == nil {
				return fixed, nil
			}
		}
		// Attempt to repair type mismatches from raw SQL exports: integer
		// booleans (Dolt TINYINT 0/1), object dependency metadata (should
		// be string), and unknown column names (timeout_ns, content_hash).
		if repaired := repairIssueTypes(data); repaired != nil {
			if fixed, err2 := decodeIssue(repaired); err2 == nil {
				return fixed, nil
			}
		}
		return nil, fmt.Errorf("flatfile: unmarshal issue %s: %w", id, err)
	}
	return issue, nil
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

// repairIssueTypes fixes type mismatches from raw Dolt SQL exports:
//   - Integer booleans (0/1) → proper JSON booleans for known bool fields
//   - Dependency metadata as object → JSON string
//   - Unknown SQL column names (timeout_ns → timeout, content_hash → ignored)
//
// Returns nil if no repairs were possible.
func repairIssueTypes(data []byte) []byte {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}

	changed := false

	// Coerce known boolean fields from number to bool.
	for _, field := range []string{"ephemeral", "is_template", "is_blocked", "no_history", "pinned"} {
		if v, ok := raw[field]; ok {
			if n, isNum := v.(float64); isNum {
				raw[field] = n != 0
				changed = true
			}
		}
	}

	// Map Dolt SQL column names to Go JSON tag names.
	if v, ok := raw["timeout_ns"]; ok {
		if _, exists := raw["timeout"]; !exists {
			raw["timeout"] = v
		}
		delete(raw, "timeout_ns")
		changed = true
	}
	// content_hash is json:"-" in the Go struct; just drop it.
	if _, ok := raw["content_hash"]; ok {
		delete(raw, "content_hash")
		changed = true
	}

	// Coerce dependency metadata from object/number to string.
	if deps, ok := raw["dependencies"].([]interface{}); ok {
		for _, dep := range deps {
			depMap, ok := dep.(map[string]interface{})
			if !ok {
				continue
			}
			if meta, ok := depMap["metadata"]; ok {
				if _, isStr := meta.(string); !isStr {
					b, err := json.Marshal(meta)
					if err == nil {
						depMap["metadata"] = string(b)
						changed = true
					}
				}
			}
		}
	}

	if !changed {
		return nil
	}

	repaired, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	return repaired
}

// issueFile is the on-disk JSON shape of an issue. It persists source_repo,
// which types.Issue excludes from JSON (json:"-") because SQL backends store
// it as a table column.
type issueFile struct {
	types.Issue
	FileSourceRepo string `json:"source_repo,omitempty"`
}

// decodeIssue unmarshals on-disk issue JSON, hoisting source_repo back onto
// the json:"-" field.
func decodeIssue(data []byte) (*types.Issue, error) {
	var f issueFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	issue := f.Issue
	issue.SourceRepo = f.FileSourceRepo
	return &issue, nil
}

// writeIssue marshals and writes an issue to disk atomically.
func (s *FlatFileStore) writeIssue(issue *types.Issue) error {
	path, err := s.safeIssueFilename(issue.ID)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(issueFile{Issue: *issue, FileSourceRepo: issue.SourceRepo}, "", "  ")
	if err != nil {
		return fmt.Errorf("flatfile: marshal issue %s: %w", issue.ID, err)
	}
	data = append(data, '\n')
	if err := atomicWriteFile(path, data); err != nil {
		return fmt.Errorf("flatfile: write issue %s: %w", issue.ID, err)
	}
	return nil
}

// loadAllIssues reads and unmarshals every issue file in the issues directory.
// Individual files that fail to parse are skipped with a warning to stderr
// so that one corrupt file does not block the entire workspace.
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
			fmt.Fprintf(os.Stderr, "warning: skipping corrupt issue %s: %v\n", id, err)
			continue
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
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

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

	// Shared with the batch path: timestamp/status defaults and closed_at
	// derivation (mirrors issueops.PrepareIssueForInsert).
	prepareIssueDefaults(issue, actor)

	if err := s.writeIssue(issue); err != nil {
		return err
	}
	if err := s.recordEvent(issue.ID, types.EventCreated, actor, "", ""); err != nil {
		return err
	}
	// Mirror issueops.PersistLabels: one label_added event per distinct label
	// carried on the created issue.
	seen := make(map[string]struct{}, len(issue.Labels))
	for _, label := range issue.Labels {
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		if err := s.recordCommentEvent(issue.ID, issueops.IsWisp(issue), types.EventLabelAdded, actor, "Added label: "+label); err != nil {
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
// Mirrors issueops.UpdateIssueInTx: field names are validated against the
// allowed set, closed_at/started_at are auto-managed on status transitions,
// and an event is always recorded (even for no-op status writes).
func (s *FlatFileStore) UpdateIssue(ctx context.Context, id string, updates map[string]interface{}, actor string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	issue, err := s.readIssue(id)
	if err != nil {
		return err
	}
	oldIssue := *issue

	for key := range updates {
		if !issueops.IsAllowedUpdateField(key) {
			return fmt.Errorf("invalid field for update: %s", key)
		}
	}
	if rawType, ok := updates["issue_type"]; ok {
		if issueType, ok := rawType.(string); ok {
			customTypes, err := s.GetCustomTypes(ctx)
			if err != nil {
				return fmt.Errorf("flatfile: get custom types for validation: %w", err)
			}
			if !types.IssueType(issueType).IsValidWithCustom(customTypes) {
				return fmt.Errorf("invalid issue type: %s", issueType)
			}
		}
	}
	if rawMeta, ok := updates["metadata"]; ok {
		metadataStr, err := storage.NormalizeMetadataValue(rawMeta)
		if err != nil {
			return fmt.Errorf("invalid metadata: %w", err)
		}
		issue.Metadata = json.RawMessage(metadataStr)
	}

	applyUpdates(issue, updates)

	newStatus, hasStatus := updateStatusValue(updates)
	if hasStatus {
		// Auto-manage closed_at (set on close, clear on reopen).
		if _, explicit := updates["closed_at"]; !explicit {
			if newStatus == types.StatusClosed {
				now := time.Now().UTC()
				issue.ClosedAt = &now
			} else if oldIssue.Status == types.StatusClosed {
				issue.ClosedAt = nil
				issue.CloseReason = ""
			}
		}
		// Auto-manage started_at (stamped once on first in_progress transition).
		if _, explicit := updates["started_at"]; !explicit {
			if newStatus == types.StatusInProgress && oldIssue.StartedAt == nil {
				now := time.Now().UTC()
				issue.StartedAt = &now
			}
		}
		// Auto-clear pinned when status transitions away from "pinned".
		if _, explicit := updates["pinned"]; !explicit && oldIssue.Pinned && newStatus != types.StatusPinned {
			issue.Pinned = false
		}
	}

	issue.UpdatedAt = time.Now().UTC()
	if err := s.writeIssue(issue); err != nil {
		return err
	}

	oldData, _ := json.Marshal(oldIssue)
	newData, _ := json.Marshal(updates)
	eventType := issueops.DetermineEventType(&oldIssue, updates)
	return s.recordEvent(id, eventType, actor, string(oldData), string(newData))
}

// updateStatusValue extracts the "status" update value, tolerating both string
// and types.Status, mirroring the type switches in issueops/update.go.
func updateStatusValue(updates map[string]interface{}) (types.Status, bool) {
	raw, ok := updates["status"]
	if !ok {
		return "", false
	}
	switch v := raw.(type) {
	case string:
		return types.Status(v), true
	case types.Status:
		return v, true
	default:
		return "", false
	}
}

// ReopenIssue reopens an issue. Mirrors sqlkit.ReopenIssue: reopen via
// UpdateIssue (which always records a status event and clears closed_at /
// close_reason via the auto-management path), then attach the reason as a
// commented event when non-empty.
func (s *FlatFileStore) ReopenIssue(ctx context.Context, id string, reason string, actor string) error {
	updates := map[string]interface{}{
		"status":      string(types.StatusOpen),
		"defer_until": nil,
	}
	if err := s.UpdateIssue(ctx, id, updates, actor); err != nil {
		return err
	}
	if reason != "" {
		return s.AddComment(ctx, id, actor, reason)
	}
	return nil
}

// UpdateIssueType changes the issue_type field. Mirrors sqlkit.UpdateIssueType,
// which delegates to UpdateIssue for validation and event recording.
func (s *FlatFileStore) UpdateIssueType(ctx context.Context, id string, issueType string, actor string) error {
	return s.UpdateIssue(ctx, id, map[string]interface{}{"issue_type": issueType}, actor)
}

// CloseIssue marks an issue as closed and records the close event. Mirrors
// issueops.CloseIssueInTx: closing an already-closed issue is a silent no-op
// that preserves the original close_reason/closed_at and mints no event.
func (s *FlatFileStore) CloseIssue(_ context.Context, id string, reason string, actor string, session string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	issue, err := s.readIssue(id)
	if err != nil {
		return err
	}
	if issue.Status == types.StatusClosed {
		return nil // AlreadyClosed: first close wins
	}

	now := time.Now().UTC()
	issue.Status = types.StatusClosed
	issue.ClosedAt = &now
	issue.CloseReason = reason
	issue.ClosedBySession = session
	issue.LeaseExpiresAt = nil
	issue.HeartbeatAt = nil
	issue.UpdatedAt = now
	if issue.Assignee == "" && actor != "" {
		issue.Assignee = actor
	}

	if err := s.writeIssue(issue); err != nil {
		return err
	}
	return s.recordEvent(id, types.EventClosed, actor, "", reason)
}

// DeleteIssue removes an issue file from disk.
func (s *FlatFileStore) DeleteIssue(_ context.Context, id string) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

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
			issue.DueAt = timePtrValue(val)
		case "defer_until":
			issue.DeferUntil = timePtrValue(val)
		case "started_at":
			issue.StartedAt = timePtrValue(val)
		case "closed_at":
			issue.ClosedAt = timePtrValue(val)
		case "close_reason":
			if v, ok := val.(string); ok {
				issue.CloseReason = v
			}
		case "closed_by_session":
			if v, ok := val.(string); ok {
				issue.ClosedBySession = v
			}
		case "source_repo":
			if v, ok := val.(string); ok {
				issue.SourceRepo = v
			}
		case "wisp":
			if v, ok := val.(bool); ok {
				issue.Ephemeral = v
			}
		case "no_history":
			if v, ok := val.(bool); ok {
				issue.NoHistory = v
			}
		case "wisp_type":
			if v, ok := val.(string); ok {
				issue.WispType = types.WispType(v)
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

// timePtrValue coerces an update value into a *time.Time, treating nil (and
// unrecognized types) as a clear.
func timePtrValue(val interface{}) *time.Time {
	switch v := val.(type) {
	case *time.Time:
		return v
	case time.Time:
		return &v
	default:
		return nil
	}
}
