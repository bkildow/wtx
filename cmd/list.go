package cmd

import (
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
	clone, err := openClone(cmd.Context())
	if err != nil {
		return err
	}
	projectRoot := clone.Root()

	filtered, err := clone.ManagedWorktrees(cmd.Context())
	if err != nil {
		return err
	}

	if len(filtered) == 0 {
		ui.Info("No worktrees found. Use 'wtx add' to create one.")
		return nil
	}

	ui.Heading("Worktrees")

	t := ui.NewTable().Headers("BRANCH", "PATH", "COMMIT")
	paths := ui.NewPathDisplay(projectRoot)
	for _, wt := range filtered {
		relPath := paths.Path(wt.Path)
		shortHead := wt.Head
		if len(shortHead) > 7 {
			shortHead = shortHead[:7]
		}
		t.Row(wt.Branch, relPath, shortHead)
	}
	ui.PrintTable(t)
	return nil
}
