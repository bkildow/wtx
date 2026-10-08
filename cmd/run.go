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

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [name] [args...]",
		Short: "Run a named project script",
		Long: `Runs a script configured under the "scripts" key of .worktree.yml, or an
executable of that name in the project's bin directory (next to shared_dir,
e.g. ~/.wtx/<name>/bin/). A scripts entry wins over a bin file of the same name.

Script paths are resolved relative to the project root (absolute and ~/ paths are
allowed). The script runs with the current worktree as its working directory
and receives these environment variables:

  WTX_SCRIPT_NAME         Name of the script being run
  WTX_PROJECT_ROOT        Project root (where .worktree.yml lives)
  WTX_SHARED_PATH         Shared directory (copy/ and symlink/)
  WTX_MAIN_BRANCH         main_branch from .worktree.yml
  WTX_MAIN_WORKTREE_PATH  Worktree checked out on the main branch (empty if none)
  WTX_WORKTREE_PATH       Path of the current worktree (empty outside a worktree)
  WTX_WORKTREE_ID         Sanitized branch name (empty outside a worktree)
  WTX_BRANCH_NAME         Branch of the current worktree (empty outside a worktree)

Deprecated WT_ aliases are also exported for compatibility.

Arguments after the script name are passed through to the script verbatim.
Because of this, wtx flags must come before the script name:

  wtx run refresh --no-cache        # --no-cache is passed to the script
  wtx run --dry-run refresh         # dry-run applies to wtx

With no name, an interactive picker lists the configured and bin scripts.`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: completeScriptNames,
		RunE:              runRun,
	}
	cmd.Flags().SetInterspersed(false)
	return cmd
}

func runRun(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	names := project.AvailableScriptNames(projectRoot, cfg)
	if len(names) == 0 {
		ui.Info("No scripts configured in .worktree.yml or found in " + ui.DisplayPath(projectRoot, project.BinPath(projectRoot, cfg)))
		return nil
	}

	var name string
	var scriptArgs []string
	if len(args) > 0 {
		name, scriptArgs = args[0], args[1:]
	} else {
		prompter := &ui.InteractivePrompter{}
		name, err = prompter.SelectScript(names)
		if err != nil {
			if ui.IsUserAbort(err) {
				return nil
			}
			return err
		}
	}

	scriptPath, err := project.ResolveScript(cfg, projectRoot, name)
	if err != nil {
		return err
	}

	sc, err := resolveScriptContext(cmd, projectRoot, cfg)
	if err != nil {
		return err
	}

	ui.Step(fmt.Sprintf("Running %s: %s", name, displayScriptPath(projectRoot, scriptPath)))

	if err := project.RunScript(ctx, project.ScriptRun{
		Name:             name,
		Path:             scriptPath,
		Args:             scriptArgs,
		Dir:              sc.dir,
		Vars:             sc.vars,
		SharedPath:       project.SharedPath(projectRoot, cfg),
		MainBranch:       cfg.MainBranch,
		MainWorktreePath: sc.mainWorktreePath,
	}, IsDryRun()); err != nil {
		return err
	}

	if !IsDryRun() {
		ui.Success("Completed: " + name)
	}
	return nil
}

// scriptContext is where a script runs and what it learns about the project.
type scriptContext struct {
	dir              string
	vars             project.TemplateVars
	mainWorktreePath string
}

// resolveScriptContext picks the working directory and template vars for a
// script run. Inside a managed worktree the script runs there with the
// worktree's branch exported; anywhere else it runs in the current directory
// with only the project root set. It also locates the main branch's worktree
// so scripts can act on it regardless of where they were invoked.
func resolveScriptContext(cmd *cobra.Command, projectRoot string, cfg *config.Config) (scriptContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return scriptContext{}, err
	}

	// Read-only lookup: use a non-dry runner so --dry-run still resolves
	// the real worktree.
	runner := git.NewRunner(project.GitDirPath(projectRoot, cfg), false)
	worktrees, err := runner.WorktreeList(cmd.Context())
	if err != nil {
		return scriptContext{}, err
	}
	filtered := filterManagedWorktrees(worktrees, projectRoot)

	sc := scriptContext{
		dir:              cwd,
		vars:             project.TemplateVars{ProjectRoot: filepath.Clean(projectRoot)},
		mainWorktreePath: resolveMainWorktreePath(worktrees, filtered, cfg.MainBranch),
	}
	if wt, ok := resolveCurrentWorktree(filtered); ok {
		sc.dir = wt.Path
		sc.vars = project.NewTemplateVars(projectRoot, wt.Path, wt.Branch)
	}
	return sc, nil
}

// resolveMainWorktreePath finds the worktree checked out on branch (empty
// for none). For wtx init projects the main worktree is the project root
// itself, which filterManagedWorktrees excludes, so fall back to the
// unfiltered list.
func resolveMainWorktreePath(all, filtered []git.WorktreeInfo, branch string) string {
	if branch == "" {
		return ""
	}
	if wt, ok := findWorktreeByBranch(filtered, branch); ok {
		return wt.Path
	}
	for _, wt := range all {
		if !wt.Bare && wt.Branch == branch {
			return wt.Path
		}
	}
	return ""
}

func completeScriptNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	projectRoot, err := project.FindRoot(cwd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cfg, err := loadProjectAt(projectRoot)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return project.AvailableScriptNames(projectRoot, cfg), cobra.ShellCompDirectiveNoFileComp
}

// displayScriptPath shows scripts under the project root as a relative path
// and everything else as-is.
func displayScriptPath(projectRoot, scriptPath string) string {
	if rel, ok := ui.RelWithin(projectRoot, scriptPath); ok {
		return rel
	}
	return scriptPath
}
