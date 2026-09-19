package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newCloneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clone <url> [<name>]",
		Short: "Clone a repo into a bare worktree project",
		Args:  cobra.RangeArgs(1, 2),
		RunE:  runClone,
	}
}

func runClone(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dry := IsDryRun()

	url := args[0]
	name := project.RepoNameFromURL(url)
	if len(args) > 1 {
		name = args[1]
	}

	projectRoot, err := filepath.Abs(name)
	if err != nil {
		return err
	}

	// Check target directory doesn't already exist
	if _, err := os.Stat(projectRoot); err == nil {
		return fmt.Errorf("directory already exists: %s", projectRoot)
	}

	// Create the project directory
	ui.Step("Creating project directory: " + name)
	if dry {
		ui.DryRunNotice("mkdir -p " + projectRoot)
	} else {
		if err := os.MkdirAll(projectRoot, 0o755); err != nil {
			return err
		}
	}

	// Clone bare
	bareDir := filepath.Join(projectRoot, config.DefaultGitDir)
	ui.Step("Cloning bare repository")
	runner := git.NewRunner(bareDir, dry)
	if err := runner.CloneBare(ctx, url, bareDir); err != nil {
		return err
	}

	// Configure remote fetch refspec
	ui.Step("Configuring remote fetch refspec")
	if err := runner.ConfigureRemoteFetch(ctx); err != nil {
		return err
	}

	if err := runner.EnableWorktreeConfig(ctx); err != nil {
		return err
	}

	// Fetch all branches
	ui.Step("Fetching remote branches")
	if err := runner.Fetch(ctx, "origin"); err != nil {
		return err
	}

	// Create scaffold directories
	cfg := config.DefaultConfig()
	cfg.MainBranch = detectDefaultBranch(ctx, runner)
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
	if err := project.EnsureGitExclude(bareDir, dry); err != nil {
		return err
	}

	// Save annotated config
	ui.Step("Writing " + config.ConfigFileName)
	if dry {
		ui.DryRunNotice("write " + filepath.Join(projectRoot, config.ConfigFileName))
	} else {
		if err := config.WriteAnnotatedWithValues(projectRoot, &cfg); err != nil {
			return err
		}
	}

	ui.Success("Project created: " + name)

	// Offer to create an initial worktree
	if err := promptInitialWorktree(ctx, runner, projectRoot, &cfg); err != nil {
		return err
	}

	return nil
}

func promptInitialWorktree(ctx context.Context, runner *git.Runner, projectRoot string, cfg *config.Config) error {
	branches, err := runner.ListRemoteBranches(ctx)
	if err != nil {
		ui.Warning("Could not list remote branches: " + err.Error())
		return nil
	}

	if len(branches) == 0 {
		return nil
	}

	prompter := &ui.InteractivePrompter{}

	confirmed, err := prompter.Confirm("Create an initial worktree?")
	if err != nil {
		return nil // User cancelled
	}
	if !confirmed {
		return nil
	}

	branch, err := prompter.SelectBranch(branches)
	if err != nil {
		return nil // User cancelled
	}

	wtPath := filepath.Join(project.WorktreesPath(projectRoot, cfg), branch)
	ui.Step("Adding worktree for branch: " + branch)
	if err := runner.WorktreeAdd(ctx, wtPath, branch); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Worktree created: %s/%s", cfg.WorktreeDir, branch))
	return nil
}
