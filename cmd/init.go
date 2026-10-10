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
	var name *string
	if cmd.Flags().Changed("name") {
		value, _ := cmd.Flags().GetString("name")
		name = &value
	}
	inRepo, _ := cmd.Flags().GetBool("in-repo")
	if inRepo && name != nil {
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
		if inRepo {
			return existingProjectError(ctx, projectRoot, cfg)
		}
		setup, err := planCloneSetup(ctx, runner, projectRoot, cfg, name)
		if errors.Is(err, project.ErrNoCloneDir) {
			return existingProjectError(ctx, projectRoot, cfg)
		}
		if err != nil {
			return err
		}
		return joinClone(ctx, runner, setup, dry)
	}

	cfg := config.DefaultConfig()
	cfg.GitDir = ".git"
	cfg.MainBranch = detectDefaultBranch(ctx, runner)
	if inRepo {
		return initInRepo(ctx, projectRoot, &cfg, dry)
	}

	setup, err := planCloneSetup(ctx, runner, projectRoot, &cfg, name)
	if err != nil {
		if !errors.Is(err, project.ErrCloneDirTaken) {
			err = fmt.Errorf("%w (or use --in-repo)", err)
		}
		return err
	}
	ui.Step("Writing " + config.ConfigFileName)
	if err := writeConfig(projectRoot, &cfg, dry); err != nil {
		return err
	}
	if err := setup.apply(ctx, runner, dry); err != nil {
		return err
	}
	paths := writtenPaths(ctx, projectRoot, setup.Config, dry)

	ui.Success("Initialized wtx project in: " + projectRoot)
	ui.Info("  Your existing checkout is the main worktree.")
	ui.Info("  Use 'wtx add <branch>' to create additional worktrees.")
	ui.Info("")
	ui.Info("  Name:           " + setup.Name.Name + " (git config " + project.NameConfigKey + ")")
	paths.print(projectRoot)
	ui.Info("")
	ui.Info("  Commit " + config.ConfigFileName + "; teammates run 'wtx init' in their clone to set it up.")
	ui.Info("  To copy ignored files such as .env into new worktrees, list them in")
	ui.Info("  .worktreeinclude (gitignore syntax) and commit it with " + config.ConfigFileName + ".")
	return nil
}

// writtenPaths reports the Clone at projectRoot as every later command
// resolves it from the .worktree.yml that init just wrote. Under dry-run
// nothing was written, so it reports the planned paths of cfg; it falls back
// to them with a warning if the written config cannot be resolved, since
// init itself already succeeded.
func writtenPaths(ctx context.Context, projectRoot string, cfg *config.Config, dry bool) clonePaths {
	planned := configuredPaths(projectRoot, cfg)
	if dry {
		return planned
	}
	written, err := config.Load(projectRoot)
	if err != nil {
		ui.Warning("Could not reload " + config.ConfigFileName + ": " + err.Error())
		return planned
	}
	clone, err := project.Resolve(ctx, projectRoot, written, project.Options{})
	if err != nil {
		ui.Warning("Could not resolve the new clone: " + err.Error())
		return planned
	}
	return clonePaths{worktrees: clone.WorktreesDir(), shared: clone.SharedDir(), bin: clone.BinDir()}
}

// clonePaths are the directories init reports.
type clonePaths struct{ worktrees, shared, bin string }

// configuredPaths are the directories a resolved cfg names for projectRoot.
func configuredPaths(projectRoot string, cfg *config.Config) clonePaths {
	return clonePaths{worktrees: project.WorktreesPath(projectRoot, cfg), shared: project.SharedPath(projectRoot, cfg), bin: project.BinPath(projectRoot, cfg)}
}

func (p clonePaths) print(root string) {
	display := func(path string) string { return ui.DisplayPath(root, path) }
	ui.Info("  Worktrees:      " + display(p.worktrees))
	ui.Info("  Shared files:   " + display(p.shared))
	ui.Info("  Refresh script: " + display(filepath.Join(p.bin, project.StarterScriptName)) + " (wtx run refresh)")
}

// existingProjectError refuses to initialize a project whose .worktree.yml
// spells out its paths (or, with --in-repo, any existing project). For an
// explicit Clone dir without an Owner marker it points at wtx doctor --fix;
// the per-clone default Clone dir is set up by a plain wtx init instead, so
// it gets no such hint. A Clone that cannot be resolved (an invalid
// wtx.name, a path that does not expand) gets the plain refusal.
func existingProjectError(ctx context.Context, projectRoot string, cfg *config.Config) error {
	err := fmt.Errorf("already a wtx project (%s exists)", config.ConfigFileName)
	clone, resolveErr := project.Resolve(ctx, projectRoot, cfg, project.Options{Quiet: true})
	if resolveErr != nil || clone.Layout() == project.BareLayout {
		return err
	}
	perClone, _ := clone.CloneDir()
	for _, dir := range clone.CloneDirs() {
		if dir == perClone {
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
func joinClone(ctx context.Context, runner *git.Runner, setup *cloneSetup, dry bool) error {
	if setup.done() {
		ui.Success("This clone is already set up: " + ui.DisplayPath(setup.root, setup.CloneDir))
		setup.printPaths()
		return nil
	}
	if err := setup.apply(ctx, runner, dry); err != nil {
		return err
	}
	ui.Success("Joined wtx project: set up this clone in " + ui.DisplayPath(setup.root, setup.CloneDir))
	ui.Info("  " + config.ConfigFileName + " is unchanged. Use 'wtx add <branch>' to create worktrees.")
	ui.Info("")
	setup.printPaths()
	return nil
}

// cloneSetup performs the per-clone part of wtx init that project.PlanClone
// planned: the ~/.wtx/<name> directory with its marker, scaffold and starter
// script, and wtx.name.
type cloneSetup struct {
	*project.ClonePlan
	root string
}

// planCloneSetup plans the clone's setup (see project.PlanClone), pointing
// at --name when the Clone dir is taken.
func planCloneSetup(ctx context.Context, runner *git.Runner, root string, cfg *config.Config, name *string) (*cloneSetup, error) {
	plan, err := project.PlanClone(ctx, runner, root, cfg, name)
	if errors.Is(err, project.ErrCloneDirTaken) {
		return nil, fmt.Errorf("%w\n  choose another directory with 'wtx init --name <other>'", err)
	}
	if err != nil {
		return nil, err
	}
	return &cloneSetup{ClonePlan: plan, root: root}, nil
}

// done reports whether the clone is fully set up already.
func (c *cloneSetup) done() bool {
	if c.SetName() {
		return false
	}
	if m, err := project.ReadMarker(c.CloneDir); err != nil || !project.SamePath(m.Root, c.root) {
		return false
	}
	for _, dir := range project.ScaffoldDirs(c.root, c.Config) {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// apply creates ~/.wtx/<name> with its marker, scaffold and starter
// script, records wtx.name and configures git excludes.
func (c *cloneSetup) apply(ctx context.Context, runner *git.Runner, dry bool) error {
	ui.Step("Setting up " + ui.DisplayPath(c.root, c.CloneDir))
	if m, err := project.ReadMarker(c.CloneDir); err != nil || !project.SamePath(m.Root, c.root) {
		if err := project.WriteMarker(c.CloneDir, c.root, dry); err != nil {
			return err
		}
	}
	if err := project.CreateScaffold(c.root, c.Config, dry); err != nil {
		return err
	}
	if err := project.WriteStarterScripts(c.root, c.Config, dry); err != nil {
		return err
	}
	if c.SetName() {
		// An invalid wtx.name (overridden by --name) never named a Clone dir.
		if c.Current != "" && project.ValidateCloneName(c.Current) == nil {
			ui.Warning("Renaming this clone from " + c.Current + " to " + c.Name.Name + ": worktrees and shared files under " +
				ui.DisplayPath(c.root, filepath.Join(filepath.Dir(c.CloneDir), c.Current)) + " stay there")
		}
		ui.Step("Recording " + project.NameConfigKey + " " + c.Name.Name + " in local git config")
		if err := runner.SetLocalConfig(ctx, project.NameConfigKey, c.Name.Name); err != nil {
			return err
		}
	}
	ui.Step("Configuring local git excludes")
	return project.EnsureGitExclude(runner.GitDir, dry)
}

func (c *cloneSetup) printPaths() {
	ui.Info("  Name:           " + c.Name.Name + " (git config " + project.NameConfigKey + ")")
	configuredPaths(c.root, c.Config).print(c.root)
}

// initInRepo initializes a project that keeps everything in .worktrees/
// inside the repository, with those paths recorded in .worktree.yml.
func initInRepo(ctx context.Context, projectRoot string, cfg *config.Config, dry bool) error {
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
	paths := writtenPaths(ctx, projectRoot, cfg, dry)

	ui.Success("Initialized wtx project in: " + projectRoot)
	ui.Info("  Your existing checkout is the main worktree.")
	ui.Info("  Use 'wtx add <branch>' to create additional worktrees.")
	ui.Info("")
	paths.print(projectRoot)
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
