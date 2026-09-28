package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
)

// An explicit --base-branch can't be honored for a branch that already
// exists, so it must fail loudly rather than silently check out the existing
// branch. This path returns before touching git.
func TestNewBranchStartPointRejectsExistingBranch(t *testing.T) {
	runner := git.NewRunner(t.TempDir(), false)
	_, err := newBranchStartPoint(context.Background(), runner, &config.Config{}, "feature/x", "develop", true)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want 'already exists' error, got %v", err)
	}
}

func TestNewBranchStartPointExistingWithoutFlag(t *testing.T) {
	runner := git.NewRunner(t.TempDir(), false)
	ref, err := newBranchStartPoint(context.Background(), runner, &config.Config{}, "feature/x", "", true)
	if err != nil || ref != "" {
		t.Fatalf("got (%q, %v), want (\"\", nil)", ref, err)
	}
}
