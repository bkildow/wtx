package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize wt in an existing git repository",
		Long:  "Wraps an existing git repository for worktree management.\nCreates .worktree.yml and a .worktrees/ directory for shared files and worktrees.",
		Args:  cobra.NoArgs,
		RunE:  runInit,
	}
}

func runInit(cmd *cobra.Command, args []string) error {
	dry := IsDryRun()

	projectRoot, err := filepath.Abs(".")
	if err != nil {
		return err
	}

	// Check that .git is a directory (not a file, which would mean we're inside a worktree)
	gitPath := filepath.Join(projectRoot, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return fmt.Errorf("no .git directory found — use 'wt clone' for bare repo setup")
	}
	if !info.IsDir() {
		return fmt.Errorf(".git is a file, not a directory — this may already be a worktree of another repo")
	}

	// Refuse if already a wt project
	if config.Exists(projectRoot) {
		return fmt.Errorf("already a wt project (%s exists)", config.ConfigFileName)
	}

	cfg := config.DefaultConfig()
	cfg.GitDir = ".git"
	cfg.WorktreeDir = ".worktrees"
	cfg.SharedDir = ".worktrees/shared"

	// Detect the repository's default branch
	gitDir := filepath.Join(projectRoot, cfg.GitDir)
	initRunner := git.NewRunner(gitDir, dry)
	cfg.MainBranch = detectDefaultBranch(cmd.Context(), initRunner)

	// Create scaffold directories
	ui.Step("Creating project scaffold")
	if err := project.CreateScaffold(projectRoot, &cfg, dry); err != nil {
		return err
	}
	if err := project.WriteStarterScripts(projectRoot, &cfg, dry); err != nil {
		return err
	}
	cfg.Scripts = project.StarterScripts(projectRoot, &cfg)

	// Configure local git excludes for wt-managed files
	ui.Step("Configuring local git excludes")
	if err := project.EnsureGitExclude(gitDir, dry); err != nil {
		return err
	}

	// Write annotated config
	ui.Step("Writing " + config.ConfigFileName)
	if dry {
		ui.DryRunNotice("write " + filepath.Join(projectRoot, config.ConfigFileName))
	} else {
		if err := config.WriteAnnotatedWithValues(projectRoot, &cfg); err != nil {
			return err
		}
	}

	ui.Success("Initialized wt project in: " + projectRoot)
	ui.Info("  Your existing checkout is the main worktree.")
	ui.Info("  Use 'wt add <branch>' to create additional worktrees.")
	ui.Info("  A starter 'wt run refresh' script was written to .worktrees/bin/refresh.")
	ui.Info("")
	ui.Info("  Consider adding to .gitignore:")
	ui.Info("    .worktrees/")
	ui.Info("  Or commit .worktrees/shared/ for team consistency and ignore only worktrees.")

	return nil
}
