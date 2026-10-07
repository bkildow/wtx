package cmd

import (
	"errors"
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
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize wtx in an existing git repository",
		Long: `Wraps an existing git repository for worktree management.

Writes .worktree.yml to the repository and keeps worktrees, shared files and
scripts outside it, in ~/.wtx/<name>/ (worktrees/, shared/, bin/). <name>
defaults to the repository directory name; override it with --name. Set
WTX_HOME to use a directory other than ~/.wtx.

--in-repo keeps everything in a .worktrees/ directory inside the repository
instead.`,
		Args: cobra.NoArgs,
		RunE: runInit,
	}
	cmd.Flags().String("name", "", "Directory name under ~/.wtx (default: repository directory name)")
	cmd.Flags().Bool("in-repo", false, "Keep worktrees and shared files in .worktrees/ inside the repository")
	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
	dry := IsDryRun()
	name, _ := cmd.Flags().GetString("name")
	inRepo, _ := cmd.Flags().GetBool("in-repo")
	if inRepo && cmd.Flags().Changed("name") {
		return errors.New("--name cannot be used with --in-repo")
	}

	projectRoot, err := filepath.Abs(".")
	if err != nil {
		return err
	}

	// Check that .git is a directory (not a file, which would mean we're inside a worktree)
	gitPath := filepath.Join(projectRoot, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return fmt.Errorf("no .git directory found — use 'wtx clone' for bare repo setup")
	}
	if !info.IsDir() {
		return fmt.Errorf(".git is a file, not a directory — this may already be a worktree of another repo")
	}

	// Refuse if already a wtx project
	if config.Exists(projectRoot) {
		return fmt.Errorf("already a wtx project (%s exists)", config.ConfigFileName)
	}

	cfg := config.DefaultConfig()
	cfg.GitDir = ".git"

	// homeDir is ~/.wtx/<name> (expanded); empty for --in-repo.
	var homeDir string
	if inRepo {
		cfg.WorktreeDir = ".worktrees"
		cfg.SharedDir = ".worktrees/shared"
	} else {
		if name == "" {
			name = filepath.Base(projectRoot)
		}
		homeDir, err = project.HomeProjectDir(name)
		if err != nil {
			return fmt.Errorf("%w (or use --in-repo)", err)
		}
		if err := project.CheckHomeDir(homeDir, projectRoot); err != nil {
			return fmt.Errorf("%w\n  choose another directory with 'wtx init --name <name>'", err)
		}
		// Written with a literal ~ so .worktree.yml stays portable.
		cfg.WorktreeDir = "~/.wtx/" + name + "/worktrees"
		cfg.SharedDir = "~/.wtx/" + name + "/shared"
	}

	// Detect the repository's default branch
	gitDir := filepath.Join(projectRoot, cfg.GitDir)
	initRunner := git.NewRunner(gitDir, dry)
	cfg.MainBranch = detectDefaultBranch(cmd.Context(), initRunner)

	// Create scaffold directories
	ui.Step("Creating project scaffold")
	if homeDir != "" {
		if err := project.WriteMarker(homeDir, projectRoot, dry); err != nil {
			return err
		}
	}
	if err := project.CreateScaffold(projectRoot, &cfg, dry); err != nil {
		return err
	}
	if err := project.WriteStarterScripts(projectRoot, &cfg, dry); err != nil {
		return err
	}
	cfg.Scripts = project.StarterScripts(&cfg)

	// Configure local git excludes for wtx-managed files
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

	display := func(p string) string { return ui.DisplayPath(projectRoot, p) }
	refresh := filepath.Join(project.BinPath(projectRoot, &cfg), project.StarterScriptName)

	ui.Success("Initialized wtx project in: " + projectRoot)
	ui.Info("  Your existing checkout is the main worktree.")
	ui.Info("  Use 'wtx add <branch>' to create additional worktrees.")
	ui.Info("")
	ui.Info("  Worktrees:      " + display(project.WorktreesPath(projectRoot, &cfg)))
	ui.Info("  Shared files:   " + display(project.SharedPath(projectRoot, &cfg)))
	ui.Info("  Refresh script: " + display(refresh) + " (wtx run refresh)")
	ui.Info("")
	if inRepo {
		ui.Info("  Consider adding to .gitignore:")
		ui.Info("    .worktrees/")
		ui.Info("  Or commit .worktrees/shared/ for team consistency and ignore only worktrees.")
	} else {
		ui.Info("  To copy ignored files such as .env into new worktrees, list them in")
		ui.Info("  .worktreeinclude (gitignore syntax) and commit it with " + config.ConfigFileName + ".")
	}

	return nil
}
