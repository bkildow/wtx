package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/ui"
)

type fakeBranchDeleter struct{ err error }

func (f fakeBranchDeleter) BranchDelete(context.Context, string, bool) error { return f.err }

func captureOutput(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := ui.Output
	ui.Output = &buf
	t.Cleanup(func() { ui.Output = prev })
	return &buf
}

func TestDeleteBranchOrKeep(t *testing.T) {
	const gitDir = "/home/me/proj/.bare"

	t.Run("merged", func(t *testing.T) {
		out := captureOutput(t)
		if kept := deleteBranchOrKeep(context.Background(), fakeBranchDeleter{}, gitDir, "feature"); kept {
			t.Error("deleted branch reported as kept")
		}
		if out.Len() != 0 {
			t.Errorf("unexpected output: %q", out)
		}
	})

	t.Run("not merged", func(t *testing.T) {
		out := captureOutput(t)
		err := fmt.Errorf("feature: %w", git.ErrBranchNotMerged)
		if kept := deleteBranchOrKeep(context.Background(), fakeBranchDeleter{err}, gitDir, "feature"); !kept {
			t.Error("unmerged branch not reported as kept")
		}
		got := out.String()
		for _, want := range []string{
			"Branch feature kept",
			"not fully merged",
			"git --git-dir /home/me/proj/.bare branch -D feature",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("output %q missing %q", got, want)
			}
		}
		if strings.Contains(got, "hint:") {
			t.Errorf("output leaks git hints: %q", got)
		}
	})

	t.Run("other failure", func(t *testing.T) {
		out := captureOutput(t)
		err := errors.New("git branch -d feature: exit status 1\nerror: branch 'feature' not found")
		if kept := deleteBranchOrKeep(context.Background(), fakeBranchDeleter{err}, gitDir, "feature"); !kept {
			t.Error("failed deletion not reported as kept")
		}
		got := out.String()
		if !strings.Contains(got, "Could not delete branch") || !strings.Contains(got, "not found") {
			t.Errorf("output %q should warn with the underlying error", got)
		}
		if strings.Contains(got, "branch -D") {
			t.Errorf("output %q should not suggest force-deleting on an unrelated failure", got)
		}
	})
}

func TestBranchDeleteCommandQuotesGitDir(t *testing.T) {
	got := branchDeleteCommand("/Users/me/My Projects/proj/.bare", "feature")
	want := "git --git-dir '/Users/me/My Projects/proj/.bare' branch -D feature"
	if got != want {
		t.Errorf("branchDeleteCommand = %q, want %q", got, want)
	}
}

func TestRemovedWorktreeMessage(t *testing.T) {
	if got := removedWorktreeMessage("feature", false); got != "Removed worktree: feature" {
		t.Errorf("deleted branch: %q", got)
	}
	if got := removedWorktreeMessage("feature", true); got != "Removed worktree: feature (branch kept)" {
		t.Errorf("kept branch: %q", got)
	}
}
