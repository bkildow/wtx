package cmd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
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
	dry := IsDryRun()

	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	gitDir := project.GitDirPath(projectRoot, cfg)
	runner := git.NewRunner(gitDir, dry)

	ui.Step("Enabling extensions.worktreeConfig on " + cfg.GitDir)
	if err := runner.EnableWorktreeConfig(ctx); err != nil {
		return err
	}

	worktrees, err := runner.WorktreeList(ctx)
	if err != nil {
		return err
	}

	inspected, repaired := 0, 0
	for _, wt := range worktrees {
		if wt.Bare {
			continue
		}
		inspected++
		ok, err := worktreeBareOverrideOK(ctx, wt.Path)
		if err != nil {
			ui.Warning(fmt.Sprintf("Could not inspect %s: %v", wt.Path, err))
			continue
		}
		if ok {
			continue
		}
		ui.Step("Repairing worktree: " + displayPath(projectRoot, wt.Path))
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

func displayPath(projectRoot, p string) string {
	if rel, err := filepath.Rel(projectRoot, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}
