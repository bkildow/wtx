package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:               "logs [name]",
		Short:             "Print a worktree's background setup log",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktreeNames,
		RunE:              runLogs,
	}
}

func runLogs(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	clone, err := openClone(ctx)
	if err != nil {
		return err
	}

	filtered, err := clone.ManagedWorktrees(ctx)
	if err != nil {
		return err
	}
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

	// Resolve read-only: printing a log must not rewrite setup state.
	state, err := project.ResolveSetupStatus(selected.Path)
	if err != nil {
		return err
	}
	path := project.SetupLog(selected.Path, state)
	if path == "" {
		switch {
		case state != nil && state.Status == project.SetupRunning:
			// The background process creates its log shortly after launch.
			ui.Info("Setup for " + selected.Branch + " has not written any output yet")
		case state != nil && state.LogFile != "":
			ui.Info("No setup log for " + selected.Branch + " (the background setup log is missing)")
		default:
			ui.Info("No setup log for " + selected.Branch + " (only background setup writes a log)")
		}
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(cmd.OutOrStdout(), f)
	return err
}
