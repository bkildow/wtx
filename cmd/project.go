package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
		ui.Error("Not a wt project (no .worktree.yml found)")
		ui.Info("  Run 'wt clone <repo-url>' to create one, or 'wt init' inside an existing repo.")
	}
	return root, err
}

// loadProject finds the project root and loads its config.
// Prints a friendly error if no project is found.
func loadProject() (string, *config.Config, error) {
	root, err := findProjectRoot()
	if err != nil {
		return "", nil, err
	}

	cfg, err := config.Load(root)
	if err != nil {
		return "", nil, err
	}

	return root, cfg, nil
}

// filterManagedWorktrees returns only worktrees that wt manages — those
// created under the worktrees directory. This excludes bare entries (from
// wt clone setups) and the main working tree at the project root (from
// wt init setups).
func filterManagedWorktrees(worktrees []git.WorktreeInfo, projectRoot string) []git.WorktreeInfo {
	absRoot := resolvePathBest(projectRoot)
	var filtered []git.WorktreeInfo
	for _, wt := range worktrees {
		if wt.Bare {
			continue
		}
		// Fast path: exact string match avoids syscall
		if wt.Path == projectRoot || resolvePathBest(wt.Path) == absRoot {
			continue
		}
		filtered = append(filtered, wt)
	}
	return filtered
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

// resolvePathBest tries to resolve symlinks; falls back to filepath.Clean.
func resolvePathBest(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

// resolveCurrentWorktree finds the managed worktree that contains the current
// working directory. Returns the matching WorktreeInfo and true, or a zero
// value and false if the cwd is not inside any managed worktree.
func resolveCurrentWorktree(filtered []git.WorktreeInfo) (git.WorktreeInfo, bool) {
	cwd, err := os.Getwd()
	if err != nil {
		return git.WorktreeInfo{}, false
	}
	currentPath := resolvePathBest(cwd)
	for _, wt := range filtered {
		wtPath := resolvePathBest(wt.Path)
		if currentPath == wtPath || strings.HasPrefix(currentPath, wtPath+string(os.PathSeparator)) {
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
			return git.WorktreeInfo{}, fmt.Errorf("not inside a managed worktree (use 'wt list' to see available worktrees)")
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
