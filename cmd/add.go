package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add [branch]",
		Short: "Create a new worktree",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runAdd,
	}
	cmd.Flags().Bool("skip-setup", false, "Skip running setup hooks after creating the worktree")
	cmd.Flags().Bool("background", false, "Run setup hooks in the background")
	cmd.Flags().Bool("foreground", false, "Run setup hooks in the foreground (blocking)")
	return cmd
}

func runAdd(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dry := IsDryRun()

	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	// Warn before the new worktree starts consuming space.
	warnLowDisk(projectRoot, cfg)

	gitDir := project.GitDirPath(projectRoot, cfg)
	runner := git.NewRunner(gitDir, dry)

	// Ensure git excludes are configured (idempotent, self-heals existing projects).
	if err := project.EnsureGitExclude(gitDir, dry); err != nil {
		ui.Warning("Could not configure git excludes: " + err.Error())
	}

	ui.Step("Fetching all remotes")
	if err := runner.FetchAll(ctx); err != nil {
		return err
	}

	var branch string
	if len(args) > 0 {
		branch = args[0]
	} else {
		prompter := &ui.InteractivePrompter{}
		branches, err := runner.ListRemoteBranches(ctx)
		if err != nil {
			return err
		}
		if len(branches) > 0 {
			branch, err = prompter.SelectBranch(branches)
			if err != nil {
				if ui.IsUserAbort(err) {
					return nil
				}
				return err
			}
		} else {
			branch, err = prompter.InputString("Branch name", "feature/my-branch")
			if err != nil {
				if ui.IsUserAbort(err) {
					return nil
				}
				return err
			}
		}
	}

	worktreePath := filepath.Join(project.WorktreesPath(projectRoot, cfg), branch)

	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree already exists: %s/%s", cfg.WorktreeDir, branch)
	}

	hasRemote, err := runner.HasRemoteBranch(ctx, branch)
	if err != nil {
		return err
	}

	hasLocal, err := runner.HasLocalBranch(ctx, branch)
	if err != nil {
		return err
	}

	ui.Step("Adding worktree for branch: " + branch)
	if hasRemote || hasLocal {
		if err := runner.WorktreeAdd(ctx, worktreePath, branch); err != nil {
			return err
		}
	} else {
		startPoint := runner.ResolveStartPoint(ctx, cfg.MainBranchOrDefault())
		if err := runner.WorktreeAddNew(ctx, worktreePath, branch, startPoint); err != nil {
			return err
		}
	}

	vars := project.NewTemplateVars(projectRoot, worktreePath, branch)
	result, err := project.Apply(projectRoot, worktreePath, cfg, dry, &vars)
	if err != nil {
		return err
	}

	msg := fmt.Sprintf("Worktree created: %s/%s (%d copied, %d symlinked)",
		cfg.WorktreeDir, branch, result.Copied, result.Symlinked)

	hasHooks := len(cfg.Setup) > 0 || len(cfg.ParallelSetup) > 0
	skipSetup, _ := cmd.Flags().GetBool("skip-setup")

	if skipSetup && hasHooks {
		if !dry {
			state := &project.SetupState{
				Status:      project.SetupSkipped,
				StartedAt:   time.Now(),
				CompletedAt: time.Now(),
			}
			if err := project.WriteSetupState(worktreePath, state); err != nil {
				ui.Warning("Failed to write setup state: " + err.Error())
			}
		}
		ui.Success(msg)
		fmt.Println(worktreePath)
		return nil
	}

	if !hasHooks {
		ui.Success(msg)
		fmt.Println(worktreePath)
		return nil
	}

	background, err := resolveBackgroundMode(cmd, cfg)
	if err != nil {
		return err
	}

	if background {
		return runSetupBackground(projectRoot, worktreePath, cfg, dry, msg)
	}

	return runSetupForeground(cmd, worktreePath, cfg, dry, msg)
}

// resolveBackgroundMode determines whether setup should run in background.
// Priority: --background flag > --foreground flag > config value > false.
func resolveBackgroundMode(cmd *cobra.Command, cfg *config.Config) (bool, error) {
	bg, _ := cmd.Flags().GetBool("background")
	fg, _ := cmd.Flags().GetBool("foreground")
	if bg && fg {
		return false, fmt.Errorf("--background and --foreground are mutually exclusive")
	}
	if bg {
		return true, nil
	}
	if fg {
		return false, nil
	}
	return cfg.BackgroundSetup, nil
}

func runSetupForeground(cmd *cobra.Command, worktreePath string, cfg *config.Config, dry bool, msg string) error {
	ctx := cmd.Context()
	startedAt := time.Now()

	var setupErr error
	setupErr = project.RunSetupHooks(ctx, cfg, worktreePath, dry, nil)
	if pErr := project.RunParallelSetupHooks(ctx, cfg, worktreePath, dry); pErr != nil {
		setupErr = errors.Join(setupErr, pErr)
	}

	if !dry {
		state := &project.SetupState{
			Status:         project.SetupComplete,
			StartedAt:      startedAt,
			CompletedAt:    time.Now(),
			HooksTotal:     len(cfg.Setup) + len(cfg.ParallelSetup),
			HooksCompleted: len(cfg.Setup) + len(cfg.ParallelSetup),
		}
		if setupErr != nil {
			state.Status = project.SetupFailed
			state.Error = setupErr.Error()
		}
		if err := project.WriteSetupState(worktreePath, state); err != nil {
			ui.Warning("Failed to write setup state: " + err.Error())
		}
	}

	elapsed := ui.FormatDuration(time.Since(startedAt))
	if setupErr != nil {
		ui.Warning(msg + " — setup hooks failed after " + elapsed)
	} else {
		ui.Success(msg + " — completed in " + elapsed)
	}
	fmt.Println(worktreePath)
	return nil
}

func runSetupBackground(projectRoot, worktreePath string, cfg *config.Config, dry bool, msg string) error {
	hooksTotal := len(cfg.Setup) + len(cfg.ParallelSetup)

	if dry {
		ui.DryRunNotice("would launch background setup process")
		ui.Success(msg)
		fmt.Println(worktreePath)
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find wtx binary: %w", err)
	}

	child := exec.Command(
		exe, "_run-setup",
		"--worktree-path", worktreePath,
		"--project-root", projectRoot,
	)
	detachProcess(child)

	// Redirect child's stdio to /dev/null so it doesn't inherit the parent's
	// pipe file descriptors. Without this, Claude Code hooks hang because the
	// child keeps the parent's stdout fd open, preventing EOF.
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", os.DevNull, err)
	}
	defer func() { _ = devNull.Close() }()
	child.Stdin = devNull
	child.Stdout = devNull
	child.Stderr = devNull

	if err := child.Start(); err != nil {
		return fmt.Errorf("failed to start background setup: %w", err)
	}

	// Write initial state with the real PID (child will overwrite with progress).
	state := &project.SetupState{
		Status:     project.SetupRunning,
		PID:        child.Process.Pid,
		StartedAt:  time.Now(),
		HooksTotal: hooksTotal,
		LogFile:    project.SetupLogPath(worktreePath),
	}
	if err := project.WriteSetupState(worktreePath, state); err != nil {
		ui.Warning("Failed to write setup state: " + err.Error())
	}

	ui.Success(msg)
	ui.Step("Setup is running in the background. Run 'wtx status' to check progress.")
	fmt.Println(worktreePath)
	return nil
}
