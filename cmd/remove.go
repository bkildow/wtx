package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "remove [name]",
		Short:             "Remove a worktree and its branch",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktreeNames,
		RunE:              runRemove,
	}
	cmd.Flags().Bool("force", false, "Remove even if worktree has uncommitted changes")
	cmd.Flags().Bool("skip-teardown", false, "Skip running teardown hooks before removing the worktree")
	return cmd
}

func runRemove(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	runner := git.NewRunner(project.GitDirPath(projectRoot, cfg), IsDryRun())

	worktrees, err := runner.WorktreeList(ctx)
	if err != nil {
		return err
	}

	filtered := filterManagedWorktrees(worktrees, projectRoot)
	mainBranch := cfg.MainBranchOrDefault()

	if len(filtered) == 0 {
		return fmt.Errorf("no worktrees found")
	}

	selected, err := selectWorktree(args, filtered)
	if err != nil {
		if ui.IsUserAbort(err) {
			return nil
		}
		return err
	}

	// If the user's shell is inside the target worktree, move the process out
	// so git can delete the directory, and remember to print the project root
	// to stdout so the shell wrapper cds there too. Dry-run deletes nothing,
	// so it needs neither the chdir nor the shell hand-off.
	relocating := isInsideWorktree(selected) && !IsDryRun()
	if relocating {
		if err := os.Chdir(projectRoot); err != nil {
			return fmt.Errorf("could not change directory to project root: %w", err)
		}
	}

	force, _ := cmd.Flags().GetBool("force")

	if !force {
		dirty, err := runner.IsWorktreeDirty(ctx, selected.Path)
		if err != nil {
			return err
		}
		if dirty {
			return fmt.Errorf("worktree has uncommitted changes (use --force to override)")
		}
	}

	// Terminate any in-progress background setup before teardown.
	terminateBackgroundSetup(selected.Path, selected.Branch, IsDryRun())

	skipTeardown, _ := cmd.Flags().GetBool("skip-teardown")
	if !skipTeardown {
		if err := project.RunTeardownHooks(ctx, cfg, selected.Path, IsDryRun()); err != nil {
			ui.Warning("Teardown hooks failed: " + err.Error())
		}
		if err := project.RunParallelTeardownHooks(ctx, cfg, selected.Path, IsDryRun()); err != nil {
			ui.Warning("Parallel teardown hooks failed: " + err.Error())
		}
	}

	isMainBranch := selected.Branch == mainBranch

	if isMainBranch {
		ui.Step("Removing worktree: " + selected.Branch + " (branch preserved in bare repo)")
	} else {
		ui.Step("Removing worktree: " + selected.Branch)
	}

	if err := runner.WorktreeRemove(ctx, selected.Path, force); err != nil {
		return err
	}

	if !isMainBranch {
		if err := runner.BranchDelete(ctx, selected.Branch, false); err != nil {
			ui.Warning("Could not delete branch: " + err.Error())
		}
	}

	ui.Success("Removed worktree: " + selected.Branch)

	// Print project root to stdout so the shell wrapper can cd the user there.
	if relocating {
		fmt.Println(projectRoot)
	}

	return nil
}

// terminateBackgroundSetup kills an in-progress background setup process.
// Under dry-run it reports the process it would signal and leaves it running.
func terminateBackgroundSetup(worktreePath, branch string, dryRun bool) {
	state, err := project.ReadSetupState(worktreePath)
	if err != nil || state == nil {
		return
	}
	if state.Status != project.SetupRunning {
		return
	}

	if dryRun {
		ui.DryRunNotice(fmt.Sprintf("would terminate in-progress setup for %s (PID %d)", branch, state.PID))
		return
	}

	ui.Warning(fmt.Sprintf("Terminating in-progress setup for %s (PID %d)", branch, state.PID))
	_ = terminateProcess(state.PID)

	// Poll for exit, then force-kill if the process doesn't terminate.
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			if !project.IsProcessAlive(state.PID) {
				return
			}
		case <-deadline:
			if project.IsProcessAlive(state.PID) {
				_ = killProcess(state.PID)
			}
			return
		}
	}
}
