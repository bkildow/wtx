package cmd

import (
	"context"
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
	cmd.Flags().String("base-branch", "", "Branch or ref to start a new branch from (default: main_branch)")
	_ = cmd.RegisterFlagCompletionFunc("base-branch", completeBranchNames)
	return cmd
}

func runAdd(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dry := IsDryRun()

	clone, err := openClone(ctx)
	if err != nil {
		return err
	}
	projectRoot, cfg := clone.Root(), clone.Config()
	// Refuse before creating anything under a ~/.wtx/<name> this clone does
	// not own (or has not set up with wtx init).
	if err := clone.CheckOwned(); err != nil {
		return err
	}

	// Warn before the new worktree starts consuming space.
	warnLowDisk(projectRoot, cfg)

	runner := clone.Runner()

	// Ensure git excludes are configured (idempotent, self-heals existing projects).
	if err := project.EnsureGitExclude(clone.GitDir(), dry); err != nil {
		ui.Warning("Could not configure git excludes: " + err.Error())
	}

	ui.Step("Fetching all remotes")
	if err := runner.FetchAll(ctx); err != nil {
		return err
	}

	baseBranch, _ := cmd.Flags().GetString("base-branch")

	// Fail on a bad base before prompting rather than after the user types a name.
	if baseBranch != "" && len(args) == 0 && !dry {
		if _, err := runner.ResolveRef(ctx, baseBranch); err != nil {
			return fmt.Errorf("invalid --base-branch: %w", err)
		}
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
		// --base-branch only makes sense for a new branch, so skip the picker
		// of existing branches and ask for a name instead.
		if len(branches) > 0 && baseBranch == "" {
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

	worktreePath := filepath.Join(clone.WorktreesDir(), branch)

	if _, err := os.Stat(worktreePath); err == nil {
		return fmt.Errorf("worktree already exists: %s", ui.DisplayPath(projectRoot, worktreePath))
	}

	hasRemote, err := runner.HasRemoteBranch(ctx, branch)
	if err != nil {
		return err
	}

	exists := hasRemote || runner.HasLocalBranch(ctx, branch)
	startPoint, err := newBranchStartPoint(ctx, runner, cfg, branch, baseBranch, exists)
	if err != nil {
		return err
	}

	ui.Step("Adding worktree for branch: " + branch)
	if exists {
		if err := runner.WorktreeAdd(ctx, worktreePath, branch); err != nil {
			return err
		}
	} else {
		if err := runner.WorktreeAddNew(ctx, worktreePath, branch, startPoint); err != nil {
			return err
		}
	}

	vars := project.NewTemplateVars(projectRoot, worktreePath, branch)
	wts, err := clone.Worktrees(ctx)
	if err != nil {
		ui.Warning("Could not list worktrees: " + err.Error())
	}
	include := includeSource(ctx, clone, wts)
	result, err := project.Apply(projectRoot, worktreePath, cfg, dry, &vars, include)
	if err != nil {
		return err
	}

	msg := fmt.Sprintf("Worktree created: %s (%d included, %d copied, %d symlinked)",
		ui.DisplayPath(projectRoot, worktreePath), result.Included, result.Copied, result.Symlinked)

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
		return runSetupBackground(projectRoot, worktreePath, branch, cfg, dry, msg)
	}

	return runSetupForeground(cmd, cfg, clone.ProjectEnv(vars, wts), dry, msg)
}

// newBranchStartPoint picks the ref a new branch is created from. An explicit
// --base-branch must resolve and is rejected for branches that already exist,
// since the existing branch's history would silently win over the flag.
// Without the flag it falls back to the configured main branch.
func newBranchStartPoint(ctx context.Context, runner *git.Runner, cfg *config.Config, branch, baseBranch string, exists bool) (string, error) {
	if baseBranch == "" {
		if exists {
			return "", nil
		}
		return runner.ResolveStartPoint(ctx, cfg.MainBranchOrDefault()), nil
	}
	if exists {
		return "", fmt.Errorf("branch %q already exists; --base-branch only applies when creating a new branch", branch)
	}
	ref, err := runner.ResolveRef(ctx, baseBranch)
	if err != nil {
		// Dry-run skips the fetch, so a branch pushed since the last fetch
		// looks missing here even though the real run would find it.
		if runner.DryRun {
			ui.Warning(fmt.Sprintf("could not resolve --base-branch (refs not fetched in dry-run): %s", err))
			return baseBranch, nil
		}
		return "", fmt.Errorf("invalid --base-branch: %w", err)
	}
	return ref, nil
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

func runSetupForeground(cmd *cobra.Command, cfg *config.Config, env project.ProjectEnv, dry bool, msg string) error {
	ctx := cmd.Context()
	worktreePath := env.Vars.WorktreePath
	startedAt := time.Now()

	var setupErr error
	setupErr = project.RunSetupHooks(ctx, cfg, env, dry, nil)
	if pErr := project.RunParallelSetupHooks(ctx, cfg, env, dry); pErr != nil {
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

func runSetupBackground(projectRoot, worktreePath, branch string, cfg *config.Config, dry bool, msg string) error {
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
		"--branch", branch,
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
	ui.Step("Setup is running in the background. Run 'wtx logs " + branch + "' to see its output or 'wtx status' to check progress.")
	fmt.Println(worktreePath)
	return nil
}
