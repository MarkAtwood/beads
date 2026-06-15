package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/flatfile"
	"github.com/steveyegge/beads/internal/types"
)

var migrateFlatfileCmd = &cobra.Command{
	Use:   "flatfile",
	Short: "Migrate from Dolt to flat-file storage",
	Long: `Migrate all issues, comments, labels, and config from the Dolt database
to flat-file JSON storage (.beads/issues/*.json).

After migration, the Dolt database is no longer needed and can be removed.
The flat-file backend uses git for sync instead of Dolt remotes.`,
	Run: func(cmd *cobra.Command, _ []string) {
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		if !dryRun {
			CheckReadonly("migrate flatfile")
		}

		beadsDir := beads.FindBeadsDir()
		if beadsDir == "" {
			FatalError("no .beads directory found; run 'bd init' first")
		}

		// Check we're not already flatfile.
		if flatfile.IsFlatFileBackend(beadsDir) {
			fmt.Println("Already using flat-file backend.")
			return
		}

		// Open current Dolt store.
		s := getStore()
		if s == nil {
			FatalError("no database connection; is Dolt running?")
		}
		ctx := context.Background()

		// Export all issues.
		issues, err := s.SearchIssues(ctx, "", types.IssueFilter{})
		if err != nil {
			FatalError("failed to list issues: %v", err)
		}
		fmt.Printf("Found %d issues to migrate.\n", len(issues))

		if dryRun {
			fmt.Println("Dry run — no changes made.")
			return
		}

		// Create flat-file store (creates dirs).
		ffStore, err := flatfile.NewFlatFileStore(beadsDir)
		if err != nil {
			FatalError("failed to create flat-file store: %v", err)
		}

		// Migrate issues.
		migrated := 0
		for _, issue := range issues {
			// Hydrate labels.
			labels, _ := s.GetLabels(ctx, issue.ID)
			issue.Labels = labels

			// Hydrate dependencies.
			deps, _ := s.GetDependencies(ctx, issue.ID)
			// Build Dependency records from resolved issues.
			var depRecords []*types.Dependency
			if dqStore, ok := s.(interface {
				GetDependencyRecords(context.Context, string) ([]*types.Dependency, error)
			}); ok {
				depRecords, _ = dqStore.GetDependencyRecords(ctx, issue.ID)
			} else {
				for _, dep := range deps {
					depRecords = append(depRecords, &types.Dependency{
						IssueID:     issue.ID,
						DependsOnID: dep.ID,
						Type:        "blocks",
					})
				}
			}
			issue.Dependencies = depRecords

			if err := ffStore.CreateIssue(ctx, issue, "migrate"); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to migrate %s: %v\n", issue.ID, err)
				continue
			}
			migrated++
		}
		fmt.Printf("Migrated %d/%d issues.\n", migrated, len(issues))

		// Migrate comments.
		commentCount := 0
		for _, issue := range issues {
			comments, err := s.GetIssueComments(ctx, issue.ID)
			if err != nil {
				continue
			}
			for _, c := range comments {
				ffStore.ImportIssueComment(ctx, c.IssueID, c.Author, c.Text, c.CreatedAt)
				commentCount++
			}
		}
		if commentCount > 0 {
			fmt.Printf("Migrated %d comments.\n", commentCount)
		}

		// Migrate config KV.
		allConfig, err := s.GetAllConfig(ctx)
		if err == nil {
			for k, v := range allConfig {
				ffStore.SetConfig(ctx, k, v)
			}
			fmt.Printf("Migrated %d config entries.\n", len(allConfig))
		}

		// Update metadata.json to flatfile backend.
		metaPath := filepath.Join(beadsDir, "metadata.json")
		metaData, _ := os.ReadFile(metaPath)
		var meta map[string]interface{}
		if err := json.Unmarshal(metaData, &meta); err != nil {
			meta = make(map[string]interface{})
		}
		meta["backend"] = "flatfile"
		delete(meta, "dolt_mode")
		delete(meta, "dolt_database")
		if meta["project_id"] == nil {
			meta["project_id"] = configfile.GenerateProjectID()
		}
		newMeta, _ := json.MarshalIndent(meta, "", "  ")
		newMeta = append(newMeta, '\n')
		if err := os.WriteFile(metaPath, newMeta, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to update metadata.json: %v\n", err)
		}

		// Update .gitignore for flat-file.
		gitignorePath := filepath.Join(beadsDir, ".gitignore")
		flatfileGitignore := "local_metadata.json\n*.tmp\nmerge_slot.json\nrepo_mtime.json\n"
		if err := os.WriteFile(gitignorePath, []byte(flatfileGitignore), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to update .gitignore: %v\n", err)
		}

		ffStore.Close()

		fmt.Println("\nMigration complete. The flat-file backend is now active.")
		fmt.Println("You can remove the Dolt data directory to reclaim space:")
		fmt.Printf("  rm -rf %s/dolt %s/embeddeddolt\n", beadsDir, beadsDir)
	},
}

func init() {
	migrateFlatfileCmd.Flags().Bool("dry-run", false, "Show what would be migrated without making changes")
	migrateCmd.AddCommand(migrateFlatfileCmd)
}
