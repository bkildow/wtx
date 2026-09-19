package cmd

import (
	"fmt"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "setup [name]",
		Short:             "Run setup hooks from .worktree.yml on an existing worktree",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktreeNames,
		RunE:              runSetup,
	}
	cmd.Flags().Bool("background", false, "Run setup hooks in the background")
	cmd.Flags().Bool("foreground", false, "Run setup hooks in the foreground (blocking)")
	return cmd
}

func runSetup(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dry := IsDryRun()

	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	if len(cfg.Setup) == 0 && len(cfg.ParallelSetup) == 0 {
		ui.Info("No setup hooks configured in .worktree.yml")
		return nil
	}

	// Resolve the target worktree with a non-dry runner: dry-run should
	// only suppress hook execution, not the read-only lookup.
	runner := git.NewRunner(project.GitDirPath(projectRoot, cfg), false)
	worktrees, err := runner.WorktreeList(ctx)
	if err != nil {
		return err
	}

	filtered := filterManagedWorktrees(worktrees, projectRoot)
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

	// Resolve read-only first: a dry run must not migrate stale legacy state.
	state, err := project.ResolveSetupStatus(selected.Path)
	if err != nil {
		return err
	}
	if state != nil && state.Status == project.SetupRunning {
		return fmt.Errorf("setup already running for %s (PID %d) — check 'wtx status'",
			selected.Branch, state.PID)
	}

	background, err := resolveBackgroundMode(cmd, cfg)
	if err != nil {
		return err
	}

	if err := project.EnsureGitExclude(project.GitDirPath(projectRoot, cfg), dry); err != nil {
		return err
	}
	if !dry {
		if _, err := project.ReconcileSetupState(selected.Path); err != nil {
			return err
		}
	}

	msg := "Running setup for: " + selected.Branch
	if background {
		return runSetupBackground(projectRoot, selected.Path, cfg, dry, msg)
	}
	return runSetupForeground(cmd, selected.Path, cfg, dry, msg)
}
