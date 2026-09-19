package cmd

import (
	"path/filepath"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show status of all worktrees",
		Args:  cobra.NoArgs,
		RunE:  runStatus,
	}
}

func runStatus(cmd *cobra.Command, args []string) error {
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

	if len(filtered) == 0 {
		ui.Info("No worktrees found. Use 'wt add' to create one.")
		warnLowDisk(projectRoot, cfg)
		return nil
	}

	ui.Heading("Worktree Status")

	t := ui.NewTable().Headers("BRANCH", "PATH", "COMMIT", "STATUS", "SETUP", "LAST COMMIT")
	for _, wt := range filtered {
		relPath, err := filepath.Rel(projectRoot, wt.Path)
		if err != nil {
			relPath = wt.Path
		}

		shortHead := wt.Head
		if len(shortHead) > 7 {
			shortHead = shortHead[:7]
		}

		dirty, err := runner.IsWorktreeDirty(ctx, wt.Path)
		if err != nil {
			return err
		}

		age, err := runner.GetLastCommitAge(ctx, wt.Path)
		if err != nil {
			return err
		}

		styledStatus := ui.StyleSuccess.Render("clean")
		if dirty {
			styledStatus = ui.StyleWarning.Render("dirty")
		}

		styledSetup := renderSetupStatus(wt.Path)

		t.Row(wt.Branch, relPath, shortHead, styledStatus, styledSetup, age)
	}
	ui.PrintTable(t)
	warnLowDisk(projectRoot, cfg)
	return nil
}

func renderSetupStatus(worktreePath string) string {
	state, err := project.ResolveSetupStatus(worktreePath)
	if err != nil || state == nil {
		return ui.StyleMuted.Render("-")
	}

	switch state.Status {
	case project.SetupRunning:
		return ui.StyleInfo.Render("In Progress")
	case project.SetupComplete:
		return ui.StyleSuccess.Render("Complete")
	case project.SetupSkipped:
		return ui.StyleWarning.Render("Skipped")
	case project.SetupFailed:
		return ui.StyleError.Render("Failed")
	default:
		return ui.StyleMuted.Render("-")
	}
}
