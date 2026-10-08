package cmd

import (
	"context"
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
		Short: "Initialize wtx in an existing git repository, or set up this clone",
		Long: `Wraps an existing git repository for worktree management.

In a repository without .worktree.yml, writes one (commit it to share it with
your team) and sets up this clone. Worktrees, shared files and scripts live
outside the repository, per clone, in ~/.wtx/<name>/ (worktrees/, shared/,
bin/). Set WTX_HOME to use a directory other than ~/.wtx.

In a clone of a repository whose committed .worktree.yml leaves worktree_dir
and shared_dir out, sets up this clone only and leaves .worktree.yml alone.
Running it again on a clone that is set up changes nothing.

<name> is recorded in the clone's local git config as wtx.name (never
committed). It defaults to the repository directory name; --name picks
another, e.g. when another repository already uses ~/.wtx/<name>/.

--in-repo instead keeps everything in a .worktrees/ directory inside the
repository and records those paths in .worktree.yml.`,
		Args: cobra.NoArgs,
		RunE: runInit,
	}
	cmd.Flags().String("name", "", "Directory name under ~/.wtx (default: wtx.name, else the repository directory name)")
	cmd.Flags().Bool("in-repo", false, "Keep worktrees and shared files in .worktrees/ inside the repository")
	return cmd
}

func runInit(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dry := IsDryRun()
	name, _ := cmd.Flags().GetString("name")
	nameSet := cmd.Flags().Changed("name")
	inRepo, _ := cmd.Flags().GetBool("in-repo")
	if inRepo && nameSet {
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
	runner := git.NewRunner(gitPath, dry)

	if config.Exists(projectRoot) {
		cfg, err := config.Load(projectRoot)
		if err != nil {
			return err
		}
		if inRepo || !cfg.HomeDefaults() {
			return existingProjectError(projectRoot, cfg)
		}
		return joinClone(ctx, runner, projectRoot, cfg, name, nameSet, dry)
	}

	cfg := config.DefaultConfig()
	cfg.GitDir = ".git"
	cfg.MainBranch = detectDefaultBranch(ctx, runner)
	if inRepo {
		return initInRepo(projectRoot, &cfg, dry)
	}

	setup, err := planCloneSetup(ctx, runner, projectRoot, &cfg, name, nameSet)
	if err != nil {
		return err
	}
	ui.Step("Writing " + config.ConfigFileName)
	if err := writeConfig(projectRoot, &cfg, dry); err != nil {
		return err
	}
	if err := setup.apply(ctx, runner, dry); err != nil {
		return err
	}

	ui.Success("Initialized wtx project in: " + projectRoot)
	ui.Info("  Your existing checkout is the main worktree.")
	ui.Info("  Use 'wtx add <branch>' to create additional worktrees.")
	ui.Info("")
	setup.printPaths()
	ui.Info("")
	ui.Info("  Commit " + config.ConfigFileName + "; teammates run 'wtx init' in their clone to set it up.")
	ui.Info("  To copy ignored files such as .env into new worktrees, list them in")
	ui.Info("  .worktreeinclude (gitignore syntax) and commit it with " + config.ConfigFileName + ".")
	return nil
}

// existingProjectError refuses to initialize a project whose .worktree.yml
// spells out its paths. For an explicit ~/.wtx/<name> directory without an
// ownership marker it points at wtx doctor --fix.
func existingProjectError(projectRoot string, cfg *config.Config) error {
	err := fmt.Errorf("already a wtx project (%s exists)", config.ConfigFileName)
	if !cfg.IsCheckoutLayout() || project.ValidatePaths(projectRoot, cfg) != nil {
		return err
	}
	for _, path := range []string{project.WorktreesPath(projectRoot, cfg), project.SharedPath(projectRoot, cfg)} {
		dir, ok := project.HomeProjectDirOf(path)
		if !ok {
			continue
		}
		if _, statErr := os.Stat(dir); statErr != nil {
			continue
		}
		if _, markerErr := project.ReadMarker(dir); errors.Is(markerErr, os.ErrNotExist) {
			return fmt.Errorf("%w\n  %s has no %s; run 'wtx doctor --fix' to record this clone as its owner", err, dir, project.MarkerFileName)
		}
	}
	return err
}

// joinClone sets up this clone of a project whose committed .worktree.yml
// leaves its paths to the per-clone default. It never rewrites the config.
func joinClone(ctx context.Context, runner *git.Runner, projectRoot string, cfg *config.Config, name string, nameSet, dry bool) error {
	setup, err := planCloneSetup(ctx, runner, projectRoot, cfg, name, nameSet)
	if err != nil {
		return err
	}
	if setup.done() {
		ui.Success("This clone is already set up: " + ui.DisplayPath(projectRoot, setup.homeDir))
		setup.printPaths()
		return nil
	}
	if err := setup.apply(ctx, runner, dry); err != nil {
		return err
	}
	ui.Success("Joined wtx project: set up this clone in " + ui.DisplayPath(projectRoot, setup.homeDir))
	ui.Info("  " + config.ConfigFileName + " is unchanged. Use 'wtx add <branch>' to create worktrees.")
	ui.Info("")
	setup.printPaths()
	return nil
}

// cloneSetup is the per-clone part of wtx init: the ~/.wtx/<name>
// directory with its marker, scaffold and starter script, and wtx.name.
type cloneSetup struct {
	root    string
	cfg     *config.Config // resolved to the ~/.wtx/<name> layout
	name    string
	homeDir string
	setName bool   // wtx.name must be written
	oldName string // wtx.name before this run, or ""
}

// planCloneSetup picks the clone's name (--name, else wtx.name, else the
// repository directory name), checks that ~/.wtx/<name> is free for this
// clone, and resolves cfg's omitted paths to it.
func planCloneSetup(ctx context.Context, runner *git.Runner, root string, cfg *config.Config, name string, nameSet bool) (*cloneSetup, error) {
	current, _, err := runner.LocalConfig(ctx, project.NameConfigKey)
	if err != nil {
		return nil, err
	}
	switch {
	case nameSet:
	case current != "":
		name = current
	default:
		name = filepath.Base(root)
	}
	homeDir, err := project.SelectHomeDir(root, name)
	if homeDir == "" {
		if config.Exists(root) {
			return nil, err
		}
		return nil, fmt.Errorf("%w (or use --in-repo)", err)
	}
	if err != nil {
		return nil, fmt.Errorf("%w\n  choose another directory with 'wtx init --name <other>'", err)
	}
	project.ApplyHomeConfigPaths(cfg, name)
	return &cloneSetup{root: root, cfg: cfg, name: name, homeDir: homeDir, setName: current != name, oldName: current}, nil
}

// done reports whether the clone is fully set up already.
func (c *cloneSetup) done() bool {
	if c.setName {
		return false
	}
	if m, err := project.ReadMarker(c.homeDir); err != nil || !project.SamePath(m.Root, c.root) {
		return false
	}
	for _, dir := range project.ScaffoldDirs(c.root, c.cfg) {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// apply creates ~/.wtx/<name> with its marker, scaffold and starter
// script, records wtx.name and configures git excludes.
func (c *cloneSetup) apply(ctx context.Context, runner *git.Runner, dry bool) error {
	ui.Step("Setting up " + ui.DisplayPath(c.root, c.homeDir))
	if m, err := project.ReadMarker(c.homeDir); err != nil || !project.SamePath(m.Root, c.root) {
		if err := project.WriteMarker(c.homeDir, c.root, dry); err != nil {
			return err
		}
	}
	if err := project.CreateScaffold(c.root, c.cfg, dry); err != nil {
		return err
	}
	if err := project.WriteStarterScripts(c.root, c.cfg, dry); err != nil {
		return err
	}
	if c.setName {
		if c.oldName != "" {
			ui.Warning("Renaming this clone from " + c.oldName + " to " + c.name + ": worktrees and shared files under " +
				ui.DisplayPath(c.root, filepath.Join(filepath.Dir(c.homeDir), c.oldName)) + " stay there")
		}
		ui.Step("Recording " + project.NameConfigKey + " " + c.name + " in local git config")
		if err := runner.SetLocalConfig(ctx, project.NameConfigKey, c.name); err != nil {
			return err
		}
	}
	ui.Step("Configuring local git excludes")
	return project.EnsureGitExclude(runner.GitDir, dry)
}

func (c *cloneSetup) printPaths() {
	display := func(p string) string { return ui.DisplayPath(c.root, p) }
	refresh := filepath.Join(project.BinPath(c.root, c.cfg), project.StarterScriptName)
	ui.Info("  Name:           " + c.name + " (git config " + project.NameConfigKey + ")")
	ui.Info("  Worktrees:      " + display(project.WorktreesPath(c.root, c.cfg)))
	ui.Info("  Shared files:   " + display(project.SharedPath(c.root, c.cfg)))
	ui.Info("  Refresh script: " + display(refresh) + " (wtx run refresh)")
}

// initInRepo initializes a project that keeps everything in .worktrees/
// inside the repository, with those paths recorded in .worktree.yml.
func initInRepo(projectRoot string, cfg *config.Config, dry bool) error {
	layout := project.InRepoConfigPaths()
	cfg.SetWorktreeDir(layout.WorktreeDir)
	cfg.SetSharedDir(layout.SharedDir)

	ui.Step("Creating project scaffold")
	if err := project.CreateScaffold(projectRoot, cfg, dry); err != nil {
		return err
	}
	if err := project.WriteStarterScripts(projectRoot, cfg, dry); err != nil {
		return err
	}
	cfg.Scripts = project.StarterScripts(cfg)

	ui.Step("Configuring local git excludes")
	if err := project.EnsureGitExclude(project.GitDirPath(projectRoot, cfg), dry); err != nil {
		return err
	}

	ui.Step("Writing " + config.ConfigFileName)
	if err := writeConfig(projectRoot, cfg, dry); err != nil {
		return err
	}

	display := func(p string) string { return ui.DisplayPath(projectRoot, p) }
	refresh := filepath.Join(project.BinPath(projectRoot, cfg), project.StarterScriptName)
	ui.Success("Initialized wtx project in: " + projectRoot)
	ui.Info("  Your existing checkout is the main worktree.")
	ui.Info("  Use 'wtx add <branch>' to create additional worktrees.")
	ui.Info("")
	ui.Info("  Worktrees:      " + display(project.WorktreesPath(projectRoot, cfg)))
	ui.Info("  Shared files:   " + display(project.SharedPath(projectRoot, cfg)))
	ui.Info("  Refresh script: " + display(refresh) + " (wtx run refresh)")
	ui.Info("")
	ui.Info("  Consider adding to .gitignore:")
	ui.Info("    .worktrees/")
	ui.Info("  Or commit .worktrees/shared/ for team consistency and ignore only worktrees.")
	return nil
}

func writeConfig(projectRoot string, cfg *config.Config, dry bool) error {
	if dry {
		ui.DryRunNotice("write " + filepath.Join(projectRoot, config.ConfigFileName))
		return nil
	}
	return config.WriteAnnotatedWithValues(projectRoot, cfg)
}
