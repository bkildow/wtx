package cmd

import (
	"fmt"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch and pull all worktrees",
		Args:  cobra.NoArgs,
		RunE:  runSync,
	}
	cmd.Flags().Bool("rebase", false, "Use rebase instead of merge when pulling")
	return cmd
}

func runSync(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	runner := git.NewRunner(project.GitDirPath(projectRoot, cfg), IsDryRun())

	ui.Step("Fetching all remotes")
	if err := runner.FetchAll(ctx); err != nil {
		return err
	}

	worktrees, err := runner.WorktreeList(ctx)
	if err != nil {
		return err
	}

	filtered := filterManagedWorktrees(worktrees, projectRoot)

	if len(filtered) == 0 {
		ui.Info("No worktrees found.")
		return nil
	}

	rebase, _ := cmd.Flags().GetBool("rebase")

	var updated, skipped, failed int

	for _, wt := range filtered {
		behind, err := runner.GetBehindCount(ctx, wt.Path)
		if err != nil {
			ui.Warning(fmt.Sprintf("%s: could not check upstream: %s", wt.Branch, err))
			failed++
			continue
		}

		if behind == 0 {
			ui.Info(fmt.Sprintf("%s: up to date", wt.Branch))
			continue
		}

		dirty, err := runner.IsWorktreeDirty(ctx, wt.Path)
		if err != nil {
			ui.Warning(fmt.Sprintf("%s: could not check status: %s", wt.Branch, err))
			failed++
			continue
		}

		if dirty {
			ui.Warning(fmt.Sprintf("%s: skipping (dirty worktree)", wt.Branch))
			skipped++
			continue
		}

		ui.Step(fmt.Sprintf("%s: pulling %d commit(s)", wt.Branch, behind))
		var pullErr error
		if rebase {
			pullErr = runner.PullRebase(ctx, wt.Path)
		} else {
			pullErr = runner.Pull(ctx, wt.Path)
		}

		if pullErr != nil {
			ui.Error(fmt.Sprintf("%s: pull failed: %s", wt.Branch, pullErr))
			failed++
			continue
		}

		updated++
	}

	ui.Success(fmt.Sprintf("Sync complete: %d updated, %d skipped, %d failed", updated, skipped, failed))

	if failed > 0 {
		return fmt.Errorf("%d worktree(s) failed to sync", failed)
	}

	return nil
}
