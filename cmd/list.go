package cmd

import (
	"path/filepath"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all worktrees",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
}

func runList(cmd *cobra.Command, args []string) error {
	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	runner := git.NewRunner(project.GitDirPath(projectRoot, cfg), IsDryRun())
	worktrees, err := runner.WorktreeList(cmd.Context())
	if err != nil {
		return err
	}

	filtered := filterManagedWorktrees(worktrees, projectRoot)

	if len(filtered) == 0 {
		ui.Info("No worktrees found. Use 'wtx add' to create one.")
		return nil
	}

	ui.Heading("Worktrees")

	t := ui.NewTable().Headers("BRANCH", "PATH", "COMMIT")
	for _, wt := range filtered {
		relPath, err := filepath.Rel(projectRoot, wt.Path)
		if err != nil {
			relPath = wt.Path
		}
		shortHead := wt.Head
		if len(shortHead) > 7 {
			shortHead = shortHead[:7]
		}
		t.Row(wt.Branch, relPath, shortHead)
	}
	ui.PrintTable(t)
	return nil
}
