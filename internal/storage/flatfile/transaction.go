package flatfile

import (
	"context"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// flatFileTx implements storage.Transaction. For flat-file, each file write is
// already atomic (write-then-rename), so the "transaction" is a logical
// grouping with no rollback of already-written files. The embedded
// *FlatFileStore promotes every store method, so the transaction reuses them
// directly; only the methods below diverge from their store namesakes.
type flatFileTx struct {
	*FlatFileStore
}

var _ storage.Transaction = (*flatFileTx)(nil)

// RunInTransaction executes fn within a logical transaction scope.
func (s *FlatFileStore) RunInTransaction(ctx context.Context, _ string, fn func(tx storage.Transaction) error) error {
	if err := s.checkClosed(); err != nil {
		return err
	}
	tx := &flatFileTx{s}
	return fn(tx)
}

// AddDependencyWithOptions ignores the transaction-scoped options: flat-file
// runs the same cycle check on every insert, so there is no cheaper bulk path
// to opt into. The store has no AddDependencyWithOptions of its own.
func (tx *flatFileTx) AddDependencyWithOptions(ctx context.Context, dep *types.Dependency, actor string, _ storage.DependencyAddOptions) error {
	return tx.AddDependency(ctx, dep, actor)
}

// GetDependencyRecords reads the issue's dependencies without the store
// method's checkClosed guard — the transaction is only handed out while the
// store is open.
func (tx *flatFileTx) GetDependencyRecords(_ context.Context, issueID string) ([]*types.Dependency, error) {
	issue, err := tx.readIssue(issueID)
	if err != nil {
		return nil, err
	}
	return issue.Dependencies, nil
}

func (tx *flatFileTx) CycleThroughEdges(_ context.Context, edges [][2]string) (string, error) {
	if len(edges) == 0 {
		return "", nil
	}

	// Build the full dependency graph from all issues on disk.
	all, err := tx.loadAllIssues()
	if err != nil {
		return "", err
	}
	adj := make(map[string][]string)
	for _, issue := range all {
		for _, dep := range issue.Dependencies {
			if dep.Type != types.DepParentChild {
				adj[issue.ID] = append(adj[issue.ID], dep.DependsOnID)
			}
		}
	}

	// DFS cycle detection: for each new edge's source, walk the graph
	// looking for a cycle that includes that edge.
	for _, e := range edges {
		target := e[1]
		start := e[0] // issue that depends on target
		// A cycle exists if we can reach start from target via the graph.
		visited := make(map[string]bool)
		var path []string
		if findPath(adj, target, start, visited, &path) {
			cycle := target
			for _, id := range path {
				cycle += " -> " + id
			}
			cycle += " -> " + target
			return cycle, nil
		}
	}
	return "", nil
}

// findPath does DFS from 'from' looking for 'to'. Returns true if reachable,
// with path containing the intermediate nodes.
func findPath(adj map[string][]string, from, to string, visited map[string]bool, path *[]string) bool {
	if from == to {
		*path = append(*path, to)
		return true
	}
	if visited[from] {
		return false
	}
	visited[from] = true
	*path = append(*path, from)
	for _, next := range adj[from] {
		if findPath(adj, next, to, visited, path) {
			return true
		}
	}
	*path = (*path)[:len(*path)-1]
	return false
}

// SetMetadata / GetMetadata mirror the store methods but without the
// checkClosed guard, matching the transaction's other read-your-writes paths.
func (tx *flatFileTx) SetMetadata(_ context.Context, key, value string) error {
	return tx.setKV(tx.configKVPath, "metadata:"+key, value)
}

func (tx *flatFileTx) GetMetadata(_ context.Context, key string) (string, error) {
	return tx.getKV(tx.configKVPath, "metadata:"+key)
}

// AddComment writes a structured comment, unlike the store's AddComment which
// records a `commented` audit event.
func (tx *flatFileTx) AddComment(ctx context.Context, issueID, actor, comment string) error {
	_, err := tx.AddIssueComment(ctx, issueID, actor, comment)
	return err
}
