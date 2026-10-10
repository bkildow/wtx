package cmd

import (
	"context"
	"fmt"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newRepairCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "repair",
		Short: "Repair worktree git config for compatibility with git 2.52+",
		Long: "Enables extensions.worktreeConfig on the common dir and writes core.bare=false " +
			"into each linked worktree's config.worktree. Idempotent — safe to re-run.\n\n" +
			"Use this on existing projects after upgrading wtx or git: git 2.52+ refuses " +
			"auto-discovery from a worktree attached to a bare common dir unless each " +
			"worktree explicitly overrides core.bare.",
		Args: cobra.NoArgs,
		RunE: runRepair,
	}
}

func runRepair(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	clone, err := openClone(ctx)
	if err != nil {
		return err
	}
	runner := clone.Runner()

	ui.Step("Enabling extensions.worktreeConfig on " + ui.DisplayPath(clone.Root(), clone.GitDir()))
	if err := runner.EnableWorktreeConfig(ctx); err != nil {
		return err
	}

	worktrees, err := clone.ManagedWorktrees(ctx)
	if err != nil {
		return err
	}

	inspected, repaired := 0, 0
	paths := ui.NewPathDisplay(clone.Root())
	for _, wt := range worktrees {
		inspected++
		ok, err := worktreeBareOverrideOK(ctx, wt.Path)
		if err != nil {
			ui.Warning(fmt.Sprintf("Could not inspect %s: %v", wt.Path, err))
			continue
		}
		if ok {
			continue
		}
		ui.Step("Repairing worktree: " + paths.Path(wt.Path))
		if err := runner.SetWorktreeBareFalse(ctx, wt.Path); err != nil {
			return err
		}
		repaired++
	}

	ui.Success(fmt.Sprintf("Repair complete: %d worktree(s) inspected, %d repaired", inspected, repaired))
	return nil
}

// worktreeBareOverrideOK reports whether the worktree's config.worktree
// already contains core.bare = false. Returns true when the override is in
// place, false when missing or when the file doesn't exist.
func worktreeBareOverrideOK(ctx context.Context, worktreePath string) (bool, error) {
	cfgPath, err := git.WorktreeConfigPath(worktreePath)
	if err != nil {
		return false, err
	}
	// A missing file reads as unset.
	value, err := git.ConfigBool(ctx, cfgPath, "core.bare")
	return value == "false", err
}
