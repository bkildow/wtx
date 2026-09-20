package cmd

import (
	"fmt"
	"os"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	isatty "github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newCdCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "cd [name]",
		Short:             "Print worktree path for shell navigation",
		Long:              "Prints the absolute path of a worktree. Use with: cd \"$(wtx cd)\"\n\nUse \"wtx cd .\" to print the path of the current worktree.\nUse \"wtx cd ..\" to navigate to the project root (same as \"wtx root\").",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktreeNames,
		RunE:              runCd,
	}
}

func runCd(cmd *cobra.Command, args []string) error {
	// Detect if stdout is a terminal (wrapper pipes stdout, so TTY means no wrapper)
	if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
		ui.Info("Tip: wtx cd prints a path but can't change your directory directly.")
		ui.Info("  Run: eval \"$(wtx shell-init zsh)\"  (or bash|fish) to set up the wrapper.")
	}

	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	// "wtx cd .." is a shortcut for "wtx root"
	if len(args) > 0 && args[0] == ".." {
		fmt.Println(projectRoot)
		return nil
	}

	runner := git.NewRunner(project.GitDirPath(projectRoot, cfg), IsDryRun())
	worktrees, err := runner.WorktreeList(cmd.Context())
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

	fmt.Println(selected.Path)
	return nil
}
