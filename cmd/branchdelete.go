package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/ui"
)

// deleteBranchOrKeep deletes a removed worktree's branch with `git branch -d`
// for remove, prune and the WorktreeRemove hook. When git refuses because the
// branch isn't fully merged, it keeps the branch, prints the exact command to
// delete it (git's own hints assume a .git the Project root may not have) and
// reports true. Other failures warn with git's error and report false, since
// the branch may already be gone.
func deleteBranchOrKeep(ctx context.Context, runner *git.Runner, branch string) (kept bool) {
	err := runner.BranchDelete(ctx, branch, false)
	switch {
	case err == nil:
		return false
	case errors.Is(err, git.ErrBranchNotMerged):
		ui.Warning(fmt.Sprintf(
			"Branch %s kept: git sees it as not fully merged.\n  To delete it anyway: %s",
			branch, branchDeleteCommand(runner.GitDir, branch),
		))
		return true
	default:
		ui.Warning("Could not delete branch: " + err.Error())
		return false
	}
}

// branchDeleteCommand is the force-delete command for branch, with --git-dir
// so it runs from anywhere, including a Bare layout's Project root.
func branchDeleteCommand(gitDir, branch string) string {
	return fmt.Sprintf("git --git-dir %s branch -D %s", shellQuote(gitDir), shellQuote(branch))
}

// removedWorktreeMessage is the success line after a worktree is removed.
func removedWorktreeMessage(branch string, branchKept bool) string {
	if branchKept {
		return "Removed worktree: " + branch + " (branch kept)"
	}
	return "Removed worktree: " + branch
}

// shellQuote single-quotes s when it holds characters a POSIX shell would
// interpret, so a printed command can be pasted as is.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_./:@+=,") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
