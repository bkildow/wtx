package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/charmbracelet/colorprofile"

	"github.com/spf13/cobra"
)

func newRunSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "_run-setup",
		Hidden: true,
		RunE:   runRunSetup,
	}
	cmd.Flags().String("worktree-path", "", "Path to the worktree")
	cmd.Flags().String("project-root", "", "Path to the project root")
	cmd.Flags().String("branch", "", "Branch checked out in the worktree")
	return cmd
}

func runRunSetup(cmd *cobra.Command, _ []string) error {
	worktreePath, _ := cmd.Flags().GetString("worktree-path")
	projectRoot, _ := cmd.Flags().GetString("project-root")
	branch, _ := cmd.Flags().GetString("branch")

	if worktreePath == "" || projectRoot == "" {
		return fmt.Errorf("--worktree-path and --project-root are required")
	}

	// Cancel context on SIGTERM/SIGINT so running hooks are stopped.
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	clone, err := project.OpenAt(ctx, projectRoot, project.Options{})
	if err != nil {
		return err
	}
	cfg := clone.Config()

	logFile, err := os.Create(project.SetupLogPath(worktreePath))
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()

	// Wrap the log file so ANSI escape codes from subprocesses (e.g.
	// composer, npm) are stripped before being written to disk.
	cpw := colorprofile.NewWriter(logFile, os.Environ())
	ui.Output = cpw
	lipgloss.Writer = cpw

	state := &project.SetupState{
		Status:     project.SetupRunning,
		PID:        os.Getpid(),
		StartedAt:  time.Now(),
		HooksTotal: len(cfg.Setup) + len(cfg.ParallelSetup),
		LogFile:    project.SetupLogPath(worktreePath),
	}
	if err := project.WriteSetupState(worktreePath, state); err != nil {
		return err
	}

	// Mark setup as failed if we exit while still "running" (e.g., signal, panic).
	defer func() {
		if state.Status == project.SetupRunning {
			state.Status = project.SetupFailed
			state.Error = "setup process terminated unexpectedly"
			state.CompletedAt = time.Now()
			_ = project.WriteSetupState(worktreePath, state)
		}
	}()

	// A listing failure only loses WTX_MAIN_WORKTREE_PATH; setup still runs.
	wts, err := clone.Worktrees(ctx)
	if err != nil {
		ui.Warning("Could not list worktrees: " + err.Error())
	}

	env := clone.ProjectEnv(project.NewTemplateVars(projectRoot, worktreePath, branch), wts)

	// Progress writes are best-effort; the final write's error is returned.
	var writeErr error
	setupErr := project.RunRecordedSetup(ctx, cfg, env, false, state, func() {
		writeErr = project.WriteSetupState(worktreePath, state)
	})

	elapsed := ui.FormatDuration(state.CompletedAt.Sub(state.StartedAt))
	if setupErr != nil {
		ui.Warning("Worktree setup failed after " + elapsed)
	} else {
		ui.Success("Worktree setup completed in " + elapsed)
	}
	return writeErr
}
