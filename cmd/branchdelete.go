package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/ui"
)

type branchDeleter interface {
	BranchDelete(ctx context.Context, branch string, force bool) error
}

// deleteBranchOrKeep deletes a removed worktree's branch with `git branch -d`
// and reports whether the branch was kept. When git refuses because the branch
// isn't fully merged, the user gets the exact command to delete it themselves
// instead of git's hints, which assume a .git directory the root may not have.
// remove, prune and the WorktreeRemove hook all share this message.
func deleteBranchOrKeep(ctx context.Context, runner branchDeleter, gitDir, branch string) (kept bool) {
	err := runner.BranchDelete(ctx, branch, false)
	switch {
	case err == nil:
		return false
	case errors.Is(err, git.ErrBranchNotMerged):
		ui.Warning(fmt.Sprintf(
			"Branch %s kept: git sees it as not fully merged.\n  To delete it anyway: %s",
			branch, branchDeleteCommand(gitDir, branch),
		))
	default:
		ui.Warning("Could not delete branch: " + err.Error())
	}
	return true
}

// branchDeleteCommand is the force-delete command for branch, with --git-dir
// so it runs from anywhere, including a bare-layout root that has no .git.
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
