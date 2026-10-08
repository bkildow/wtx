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
)

const dotAlias = "."

// findProjectRoot resolves the current directory and walks up to find the project root.
// Prints a friendly error if no project is found.
func findProjectRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	root, err := project.FindRoot(cwd)
	if errors.Is(err, config.ErrConfigNotFound) {
		printNotAProject()
	}
	return root, err
}

// openClone opens the Clone containing the current directory, honoring
// --dry-run (see project.Open). Outside a Project it prints a friendly error
// and returns config.ErrConfigNotFound.
func openClone(ctx context.Context) (*project.Clone, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	c, err := project.Open(ctx, cwd, project.Options{DryRun: IsDryRun()})
	if errors.Is(err, config.ErrConfigNotFound) {
		printNotAProject()
	}
	return c, err
}

func printNotAProject() {
	ui.Error("Not a wtx project (no .worktree.yml found)")
	ui.Info("  Run 'wtx clone <repo-url>' to create one, or 'wtx init' inside an existing repo.")
}

// detectDefaultBranch asks git for the remote's default branch and falls back
// to config.DefaultMainBranch on error.
func detectDefaultBranch(ctx context.Context, runner git.Git) string {
	branch, err := runner.GetDefaultBranch(ctx)
	if err != nil {
		ui.Warning("Could not detect default branch, defaulting to 'main'")
		return config.DefaultMainBranch
	}
	return branch
}

// branchFromWorktreePath returns the branch checked out at worktreePath
// according to git (worktrees, from WorktreeList). When git has no entry with
// a branch for that path, it derives the name from the worktree's location
// under worktreesDir. Paths are canonicalized so an expanded "~" or WTX_HOME
// path matches however the caller spelled it (e.g. macOS /var vs
// /private/var).
func branchFromWorktreePath(worktrees []git.WorktreeInfo, worktreesDir, worktreePath string) (string, error) {
	path := ui.CanonicalPath(worktreePath)
	for _, wt := range worktrees {
		if !wt.Bare && wt.Branch != "" && ui.CanonicalPath(wt.Path) == path {
			return wt.Branch, nil
		}
	}
	rel, err := filepath.Rel(ui.CanonicalPath(worktreesDir), path)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// resolveCurrentWorktree finds the managed worktree that contains the current
// working directory. Returns the matching WorktreeInfo and true, or a zero
// value and false if the cwd is not inside any managed worktree.
func resolveCurrentWorktree(filtered []git.WorktreeInfo) (git.WorktreeInfo, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return git.WorktreeInfo{}, false
	}
	currentPath := ui.CanonicalPath(cwd)
	for _, wt := range filtered {
		// The worktree root itself (".") counts as inside it.
		if ui.Within(ui.CanonicalPath(wt.Path), currentPath) {
			return wt, true
		}
	}
	return git.WorktreeInfo{}, false
}

// isInsideWorktree reports whether the current working directory is inside
// the given worktree path (at the root or in a subdirectory).
func isInsideWorktree(wt git.WorktreeInfo) bool {
	_, ok := resolveCurrentWorktree([]git.WorktreeInfo{wt})
	return ok
}

// findWorktreeByBranch looks up a worktree by branch name in the given list.
func findWorktreeByBranch(filtered []git.WorktreeInfo, branch string) (git.WorktreeInfo, bool) {
	for _, wt := range filtered {
		if wt.Branch == branch {
			return wt, true
		}
	}
	return git.WorktreeInfo{}, false
}

// selectWorktree resolves a worktree from command args: "." for the current
// worktree, a branch name for an exact match, or an interactive prompt when
// no argument is provided.
func selectWorktree(args []string, filtered []git.WorktreeInfo) (git.WorktreeInfo, error) {
	switch {
	case len(args) > 0 && args[0] == dotAlias:
		wt, ok := resolveCurrentWorktree(filtered)
		if !ok {
			return git.WorktreeInfo{}, fmt.Errorf("not inside a managed worktree (use 'wtx list' to see available worktrees)")
		}
		return wt, nil
	case len(args) > 0:
		wt, ok := findWorktreeByBranch(filtered, args[0])
		if !ok {
			return git.WorktreeInfo{}, fmt.Errorf("worktree not found: %s", args[0])
		}
		return wt, nil
	default:
		names := make([]string, len(filtered))
		for i, wt := range filtered {
			names[i] = wt.Branch
		}
		prompter := &ui.InteractivePrompter{}
		name, err := prompter.SelectWorktree(names)
		if err != nil {
			return git.WorktreeInfo{}, err
		}
		wt, _ := findWorktreeByBranch(filtered, name)
		return wt, nil
	}
}
