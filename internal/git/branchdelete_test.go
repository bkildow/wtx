package git

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestIsNotFullyMerged(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{"modern git", "error: the branch 'feature' is not fully merged\nhint: If you are sure you want to delete it, run 'git branch -D feature'\n", true},
		{"older git", "error: The branch 'feature' is not fully merged.\nIf you are sure you want to delete it, run 'git branch -D feature'.\n", true},
		{"missing branch", "error: branch 'feature' not found\n", false},
		{"checked out", "error: cannot delete branch 'feature' used by worktree at '/tmp/x'\n", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNotFullyMerged(tt.stderr); got != tt.want {
				t.Errorf("isNotFullyMerged(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
	}
}

func TestBranchDeleteNotMerged(t *testing.T) {
	r := newMergeRepo(t)
	r.git("checkout", "-qb", "feature")
	r.commit("feature.txt", "unmerged\n")
	r.git("checkout", "-q", "main")

	err := r.runner().BranchDelete(context.Background(), "feature", false)
	if !errors.Is(err, ErrBranchNotMerged) {
		t.Fatalf("BranchDelete on an unmerged branch = %v, want ErrBranchNotMerged", err)
	}
	if strings.Contains(err.Error(), "hint:") {
		t.Errorf("BranchDelete error leaks git's hint lines: %q", err)
	}
	if ok, _ := r.runner().HasLocalBranch(context.Background(), "feature"); !ok {
		t.Error("unmerged branch was deleted")
	}
}

func TestBranchDeleteMerged(t *testing.T) {
	r := newMergeRepo(t)
	r.git("branch", "feature")

	if err := r.runner().BranchDelete(context.Background(), "feature", false); err != nil {
		t.Fatalf("BranchDelete on a merged branch: %v", err)
	}
	if ok, _ := r.runner().HasLocalBranch(context.Background(), "feature"); ok {
		t.Error("merged branch still exists")
	}
}

func TestBranchDeleteOtherFailure(t *testing.T) {
	r := newMergeRepo(t)

	err := r.runner().BranchDelete(context.Background(), "missing", false)
	if err == nil {
		t.Fatal("BranchDelete on a missing branch returned nil")
	}
	if errors.Is(err, ErrBranchNotMerged) {
		t.Errorf("missing branch classified as not merged: %v", err)
	}
}
