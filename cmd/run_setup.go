package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/bkildow/wtx/internal/config"
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
	return cmd
}

func runRunSetup(cmd *cobra.Command, _ []string) error {
	worktreePath, _ := cmd.Flags().GetString("worktree-path")
	projectRoot, _ := cmd.Flags().GetString("project-root")

	if worktreePath == "" || projectRoot == "" {
		return fmt.Errorf("--worktree-path and --project-root are required")
	}

	// Cancel context on SIGTERM/SIGINT so running hooks are stopped.
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg, err := config.Load(projectRoot)
	if err != nil {
		return err
	}

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

	hooksTotal := len(cfg.Setup) + len(cfg.ParallelSetup)

	state := &project.SetupState{
		Status:     project.SetupRunning,
		PID:        os.Getpid(),
		StartedAt:  time.Now(),
		HooksTotal: hooksTotal,
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

	var setupErr error

	// Run serial hooks with progress tracking.
	onProgress := func(index int, cmdStr string, hookErr error) {
		state.HooksCompleted = index + 1
		_ = project.WriteSetupState(worktreePath, state)
	}
	setupErr = project.RunSetupHooks(ctx, cfg, worktreePath, false, onProgress)

	// Run parallel hooks as a batch.
	if pErr := project.RunParallelSetupHooks(ctx, cfg, worktreePath, false); pErr != nil {
		setupErr = errors.Join(setupErr, pErr)
	}
	state.HooksCompleted = hooksTotal

	if setupErr != nil {
		state.Status = project.SetupFailed
		state.Error = setupErr.Error()
	} else {
		state.Status = project.SetupComplete
	}
	state.CompletedAt = time.Now()

	elapsed := ui.FormatDuration(state.CompletedAt.Sub(state.StartedAt))
	if setupErr != nil {
		ui.Warning("Worktree setup failed after " + elapsed)
	} else {
		ui.Success("Worktree setup completed in " + elapsed)
	}

	return project.WriteSetupState(worktreePath, state)
}
