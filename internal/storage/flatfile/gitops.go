// gitops.go implements VersionControl, HistoryViewer, RemoteStore, SyncStore,
// and FederationStore interfaces using git commands on the .beads/ directory.
package flatfile

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// gitDir returns the repo root (parent of .beads/).
func (s *FlatFileStore) gitDir() string {
	return filepath.Dir(s.beadsDir)
}

// git runs a git command in the repo root and returns stdout.
func (s *FlatFileStore) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.gitDir()
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), string(ee.Stderr))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ── VersionControl ───────────────────────────────────────────────────

func (s *FlatFileStore) Branch(_ context.Context, name string) error {
	_, err := s.git("branch", name)
	return err
}

func (s *FlatFileStore) Checkout(_ context.Context, branch string) error {
	_, err := s.git("checkout", branch)
	return err
}

func (s *FlatFileStore) CurrentBranch(_ context.Context) (string, error) {
	return s.git("rev-parse", "--abbrev-ref", "HEAD")
}

func (s *FlatFileStore) DeleteBranch(_ context.Context, branch string) error {
	_, err := s.git("branch", "-d", branch)
	return err
}

func (s *FlatFileStore) ListBranches(_ context.Context) ([]string, error) {
	out, err := s.git("branch", "--list", "--format=%(refname:short)")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

func (s *FlatFileStore) Commit(_ context.Context, message string) error {
	s.git("add", ".beads/")
	_, err := s.git("commit", "-m", message, "--", ".beads/")
	return err
}

func (s *FlatFileStore) CommitWithConfig(ctx context.Context, message string) error {
	return s.Commit(ctx, message)
}

func (s *FlatFileStore) CommitPending(_ context.Context, _ string) (bool, error) {
	out, err := s.git("status", "--porcelain", ".beads/")
	if err != nil {
		return false, err
	}
	if out == "" {
		return false, nil
	}
	s.git("add", ".beads/")
	_, err = s.git("commit", "-m", "bd: auto-commit pending changes")
	return err == nil, err
}

func (s *FlatFileStore) CommitExists(_ context.Context, commitHash string) (bool, error) {
	_, err := s.git("cat-file", "-t", commitHash)
	return err == nil, nil
}

func (s *FlatFileStore) GetCurrentCommit(_ context.Context) (string, error) {
	return s.git("rev-parse", "HEAD")
}

func (s *FlatFileStore) Status(_ context.Context) (*storage.Status, error) {
	out, err := s.git("status", "--porcelain", ".beads/")
	if err != nil {
		return &storage.Status{}, nil
	}
	var staged, unstaged []storage.StatusEntry
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		indexStatus := line[0]
		workStatus := line[1]
		file := strings.TrimSpace(line[3:])

		status := "modified"
		if indexStatus == '?' || workStatus == '?' {
			status = "new"
		} else if indexStatus == 'D' || workStatus == 'D' {
			status = "deleted"
		}

		if indexStatus != ' ' && indexStatus != '?' {
			staged = append(staged, storage.StatusEntry{Table: file, Status: status})
		}
		if workStatus != ' ' {
			unstaged = append(unstaged, storage.StatusEntry{Table: file, Status: status})
		}
	}
	return &storage.Status{Staged: staged, Unstaged: unstaged}, nil
}

func (s *FlatFileStore) Log(_ context.Context, limit int) ([]storage.CommitInfo, error) {
	args := []string{"log", fmt.Sprintf("-%d", limit), "--format=%H|%an|%ae|%aI|%s", "--", ".beads/"}
	out, err := s.git(args...)
	if err != nil {
		return nil, nil // no commits yet
	}
	if out == "" {
		return nil, nil
	}

	var commits []storage.CommitInfo
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 5 {
			continue
		}
		date, _ := time.Parse(time.RFC3339, parts[3])
		commits = append(commits, storage.CommitInfo{
			Hash:    parts[0],
			Author:  parts[1],
			Email:   parts[2],
			Date:    date,
			Message: parts[4],
		})
	}
	return commits, nil
}

func (s *FlatFileStore) Merge(_ context.Context, branch string) ([]storage.Conflict, error) {
	_, err := s.git("merge", branch)
	if err != nil {
		// Check for conflicts
		conflicts, _ := s.GetConflicts(context.Background())
		return conflicts, err
	}
	return nil, nil
}

func (s *FlatFileStore) GetConflicts(_ context.Context) ([]storage.Conflict, error) {
	out, _ := s.git("diff", "--name-only", "--diff-filter=U")
	if out == "" {
		return nil, nil
	}
	var conflicts []storage.Conflict
	for _, file := range strings.Split(out, "\n") {
		if strings.HasPrefix(file, ".beads/issues/") {
			id := strings.TrimSuffix(strings.TrimPrefix(file, ".beads/issues/"), ".json")
			conflicts = append(conflicts, storage.Conflict{IssueID: id})
		}
	}
	return conflicts, nil
}

func (s *FlatFileStore) ResolveConflicts(_ context.Context, _ string, strategy string) error {
	switch strategy {
	case "ours":
		_, err := s.git("checkout", "--ours", ".beads/")
		return err
	case "theirs":
		_, err := s.git("checkout", "--theirs", ".beads/")
		return err
	default:
		return fmt.Errorf("flatfile: unknown conflict strategy: %s", strategy)
	}
}

// ── HistoryViewer ────────────────────────────────────────────────────

func (s *FlatFileStore) History(_ context.Context, issueID string) ([]*storage.HistoryEntry, error) {
	file := fmt.Sprintf(".beads/issues/%s.json", issueID)
	out, err := s.git("log", "--format=%H|%an|%aI", "--", file)
	if err != nil || out == "" {
		return nil, nil
	}

	var entries []*storage.HistoryEntry
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		date, _ := time.Parse(time.RFC3339, parts[2])
		entry := &storage.HistoryEntry{
			CommitHash: parts[0],
			Committer:  parts[1],
			CommitDate: date,
		}
		// Try to load the issue at this commit.
		if content, err := s.git("show", parts[0]+":"+file); err == nil {
			var issue types.Issue
			if jsonErr := json.Unmarshal([]byte(content), &issue); jsonErr == nil {
				entry.Issue = &issue
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *FlatFileStore) AsOf(_ context.Context, issueID string, ref string) (*types.Issue, error) {
	file := fmt.Sprintf(".beads/issues/%s.json", issueID)
	content, err := s.git("show", ref+":"+file)
	if err != nil {
		return nil, storage.ErrNotFound
	}
	var issue types.Issue
	if err := json.Unmarshal([]byte(content), &issue); err != nil {
		return nil, err
	}
	return &issue, nil
}

func (s *FlatFileStore) Diff(_ context.Context, fromRef, toRef string) ([]*storage.DiffEntry, error) {
	out, err := s.git("diff", "--name-status", fromRef+".."+toRef, "--", ".beads/issues/")
	if err != nil || out == "" {
		return nil, nil
	}

	var entries []*storage.DiffEntry
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		status := parts[0]
		file := parts[1]
		if !strings.HasPrefix(file, ".beads/issues/") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(file, ".beads/issues/"), ".json")

		var diffType string
		switch status {
		case "A":
			diffType = "added"
		case "M":
			diffType = "modified"
		case "D":
			diffType = "removed"
		default:
			diffType = "modified"
		}

		entries = append(entries, &storage.DiffEntry{
			IssueID:  id,
			DiffType: diffType,
		})
	}
	return entries, nil
}

// ── RemoteStore ──────────────────────────────────────────────────────

func (s *FlatFileStore) AddRemote(_ context.Context, name, url string) error {
	_, err := s.git("remote", "add", name, url)
	return err
}

func (s *FlatFileStore) RemoveRemote(_ context.Context, name string) error {
	_, err := s.git("remote", "remove", name)
	return err
}

func (s *FlatFileStore) HasRemote(_ context.Context, name string) (bool, error) {
	out, err := s.git("remote")
	if err != nil {
		return false, err
	}
	for _, r := range strings.Split(out, "\n") {
		if strings.TrimSpace(r) == name {
			return true, nil
		}
	}
	return false, nil
}

func (s *FlatFileStore) ListRemotes(_ context.Context) ([]storage.RemoteInfo, error) {
	out, err := s.git("remote", "-v")
	if err != nil || out == "" {
		return nil, nil
	}
	seen := make(map[string]bool)
	var remotes []storage.RemoteInfo
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		name := parts[0]
		if seen[name] {
			continue
		}
		seen[name] = true
		remotes = append(remotes, storage.RemoteInfo{Name: name, URL: parts[1]})
	}
	return remotes, nil
}

func (s *FlatFileStore) Push(_ context.Context) error {
	s.git("add", ".beads/")
	_, err := s.git("push")
	return err
}

func (s *FlatFileStore) Pull(_ context.Context) error {
	_, err := s.git("pull")
	return err
}

func (s *FlatFileStore) ForcePush(_ context.Context) error {
	s.git("add", ".beads/")
	_, err := s.git("push", "--force")
	return err
}

func (s *FlatFileStore) PushRemote(_ context.Context, remote string, force bool) error {
	args := []string{"push", remote}
	if force {
		args = append(args, "--force")
	}
	_, err := s.git(args...)
	return err
}

func (s *FlatFileStore) PullRemote(_ context.Context, remote string) error {
	_, err := s.git("pull", remote)
	return err
}

func (s *FlatFileStore) Fetch(_ context.Context, peer string) error {
	_, err := s.git("fetch", peer)
	return err
}

func (s *FlatFileStore) PushTo(_ context.Context, peer string) error {
	s.git("add", ".beads/")
	_, err := s.git("push", peer)
	return err
}

func (s *FlatFileStore) PullFrom(_ context.Context, peer string) ([]storage.Conflict, error) {
	_, err := s.git("pull", peer)
	if err != nil {
		conflicts, _ := s.GetConflicts(context.Background())
		return conflicts, err
	}
	return nil, nil
}

// ── SyncStore ────────────────────────────────────────────────────────

func (s *FlatFileStore) Sync(_ context.Context, peer string, _ string) (*storage.SyncResult, error) {
	start := time.Now()
	result := &storage.SyncResult{Peer: peer, StartTime: start}

	_, err := s.git("fetch", peer)
	if err != nil {
		result.Error = err
		result.EndTime = time.Now()
		return result, err
	}
	result.Fetched = true

	_, err = s.git("merge", peer+"/main")
	if err != nil {
		result.Conflicts, _ = s.GetConflicts(context.Background())
		result.Error = err
	} else {
		result.Merged = true
	}

	s.git("add", ".beads/")
	_, pushErr := s.git("push", peer)
	if pushErr != nil {
		result.PushError = pushErr
	} else {
		result.Pushed = true
	}

	result.EndTime = time.Now()
	return result, result.Error
}

func (s *FlatFileStore) SyncStatus(_ context.Context, peer string) (*storage.SyncStatus, error) {
	out, err := s.git("rev-list", "--left-right", "--count", "HEAD..."+peer+"/main")
	if err != nil {
		return &storage.SyncStatus{Peer: peer}, nil
	}
	parts := strings.Fields(out)
	ahead, behind := 0, 0
	if len(parts) >= 2 {
		ahead, _ = strconv.Atoi(parts[0])
		behind, _ = strconv.Atoi(parts[1])
	}
	return &storage.SyncStatus{
		Peer:        peer,
		LocalAhead:  ahead,
		LocalBehind: behind,
	}, nil
}

// ── FederationStore (stub) ───────────────────────────────────────────

var errFederationNotSupported = fmt.Errorf("flatfile: federation not supported (use git remotes instead)")

func (s *FlatFileStore) AddFederationPeer(_ context.Context, _ *storage.FederationPeer) error {
	return errFederationNotSupported
}

func (s *FlatFileStore) GetFederationPeer(_ context.Context, _ string) (*storage.FederationPeer, error) {
	return nil, errFederationNotSupported
}

func (s *FlatFileStore) ListFederationPeers(_ context.Context) ([]*storage.FederationPeer, error) {
	return nil, nil
}

func (s *FlatFileStore) RemoveFederationPeer(_ context.Context, _ string) error {
	return errFederationNotSupported
}

