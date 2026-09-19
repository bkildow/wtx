package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bkildow/wtx/internal/git"
)

func TestFilterManagedWorktrees(t *testing.T) {
	tests := []struct {
		name        string
		worktrees   []git.WorktreeInfo
		projectRoot string
		wantCount   int
		wantBranch  []string
	}{
		{
			name: "bare repo setup filters bare entry",
			worktrees: []git.WorktreeInfo{
				{Path: "/proj/.bare", Branch: "", Bare: true},
				{Path: "/proj/worktrees/develop", Branch: "develop"},
			},
			projectRoot: "/proj",
			wantCount:   1,
			wantBranch:  []string{"develop"},
		},
		{
			name: "non-bare setup filters project root",
			worktrees: []git.WorktreeInfo{
				{Path: "/proj", Branch: "main"},
				{Path: "/proj/worktrees/feature", Branch: "feature"},
			},
			projectRoot: "/proj",
			wantCount:   1,
			wantBranch:  []string{"feature"},
		},
		{
			name: "non-bare setup with no additional worktrees",
			worktrees: []git.WorktreeInfo{
				{Path: "/proj", Branch: "main"},
			},
			projectRoot: "/proj",
			wantCount:   0,
		},
		{
			name: "multiple managed worktrees",
			worktrees: []git.WorktreeInfo{
				{Path: "/proj", Branch: "main"},
				{Path: "/proj/worktrees/feat-a", Branch: "feat-a"},
				{Path: "/proj/worktrees/feat-b", Branch: "feat-b"},
			},
			projectRoot: "/proj",
			wantCount:   2,
			wantBranch:  []string{"feat-a", "feat-b"},
		},
		{
			name:        "empty worktree list",
			worktrees:   nil,
			projectRoot: "/proj",
			wantCount:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterManagedWorktrees(tt.worktrees, tt.projectRoot)
			if len(got) != tt.wantCount {
				t.Errorf("got %d worktrees, want %d", len(got), tt.wantCount)
			}
			for i, branch := range tt.wantBranch {
				if i >= len(got) {
					break
				}
				if got[i].Branch != branch {
					t.Errorf("got[%d].Branch = %q, want %q", i, got[i].Branch, branch)
				}
			}
		})
	}
}

func TestResolveCurrentWorktree(t *testing.T) {
	// Create temp directory structure mimicking worktrees.
	tmp := t.TempDir()
	wtA := filepath.Join(tmp, "worktrees", "feat-a")
	wtB := filepath.Join(tmp, "worktrees", "feat-b")
	subdir := filepath.Join(wtA, "src", "pkg")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtB, 0o755); err != nil {
		t.Fatal(err)
	}

	filtered := []git.WorktreeInfo{
		{Path: wtA, Branch: "feat-a"},
		{Path: wtB, Branch: "feat-b"},
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	t.Run("at worktree root", func(t *testing.T) {
		if err := os.Chdir(wtA); err != nil {
			t.Fatal(err)
		}
		wt, ok := resolveCurrentWorktree(filtered)
		if !ok {
			t.Fatal("expected to find worktree")
		}
		if wt.Branch != "feat-a" {
			t.Errorf("got branch %q, want feat-a", wt.Branch)
		}
	})

	t.Run("in worktree subdirectory", func(t *testing.T) {
		if err := os.Chdir(subdir); err != nil {
			t.Fatal(err)
		}
		wt, ok := resolveCurrentWorktree(filtered)
		if !ok {
			t.Fatal("expected to find worktree")
		}
		if wt.Branch != "feat-a" {
			t.Errorf("got branch %q, want feat-a", wt.Branch)
		}
	})

	t.Run("outside any worktree", func(t *testing.T) {
		if err := os.Chdir(tmp); err != nil {
			t.Fatal(err)
		}
		_, ok := resolveCurrentWorktree(filtered)
		if ok {
			t.Error("expected not to find a worktree at project root")
		}
	})

	t.Run("correct worktree among multiple", func(t *testing.T) {
		if err := os.Chdir(wtB); err != nil {
			t.Fatal(err)
		}
		wt, ok := resolveCurrentWorktree(filtered)
		if !ok {
			t.Fatal("expected to find worktree")
		}
		if wt.Branch != "feat-b" {
			t.Errorf("got branch %q, want feat-b", wt.Branch)
		}
	})
}

func TestFindWorktreeByBranch(t *testing.T) {
	filtered := []git.WorktreeInfo{
		{Path: "/proj/worktrees/feat-a", Branch: "feat-a"},
		{Path: "/proj/worktrees/feat-b", Branch: "feat-b"},
	}

	t.Run("found", func(t *testing.T) {
		wt, ok := findWorktreeByBranch(filtered, "feat-b")
		if !ok {
			t.Fatal("expected to find worktree")
		}
		if wt.Branch != "feat-b" {
			t.Errorf("got branch %q, want feat-b", wt.Branch)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, ok := findWorktreeByBranch(filtered, "nope")
		if ok {
			t.Error("expected not to find worktree")
		}
	})

	t.Run("empty list", func(t *testing.T) {
		_, ok := findWorktreeByBranch(nil, "feat-a")
		if ok {
			t.Error("expected not to find worktree in empty list")
		}
	})
}

func TestSelectWorktree(t *testing.T) {
	tmp := t.TempDir()
	wtA := filepath.Join(tmp, "worktrees", "feat-a")
	wtB := filepath.Join(tmp, "worktrees", "feat-b")
	if err := os.MkdirAll(wtA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtB, 0o755); err != nil {
		t.Fatal(err)
	}

	filtered := []git.WorktreeInfo{
		{Path: wtA, Branch: "feat-a"},
		{Path: wtB, Branch: "feat-b"},
	}

	t.Run("by name", func(t *testing.T) {
		wt, err := selectWorktree([]string{"feat-b"}, filtered)
		if err != nil {
			t.Fatal(err)
		}
		if wt.Branch != "feat-b" {
			t.Errorf("got branch %q, want feat-b", wt.Branch)
		}
	})

	t.Run("by name not found", func(t *testing.T) {
		_, err := selectWorktree([]string{"nope"}, filtered)
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("dot alias resolves current worktree", func(t *testing.T) {
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		if err := os.Chdir(wtA); err != nil {
			t.Fatal(err)
		}
		wt, err := selectWorktree([]string{"."}, filtered)
		if err != nil {
			t.Fatal(err)
		}
		if wt.Branch != "feat-a" {
			t.Errorf("got branch %q, want feat-a", wt.Branch)
		}
	})

	t.Run("dot alias outside worktree errors", func(t *testing.T) {
		origDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chdir(origDir) })

		if err := os.Chdir(tmp); err != nil {
			t.Fatal(err)
		}
		_, err = selectWorktree([]string{"."}, filtered)
		if err == nil {
			t.Fatal("expected error when not inside a worktree")
		}
	})
}

func TestIsInsideWorktree(t *testing.T) {
	tmp := t.TempDir()
	wtA := filepath.Join(tmp, "worktrees", "feat-a")
	wtB := filepath.Join(tmp, "worktrees", "feat-b")
	if err := os.MkdirAll(wtA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtB, 0o755); err != nil {
		t.Fatal(err)
	}

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	t.Run("inside target worktree", func(t *testing.T) {
		if err := os.Chdir(wtA); err != nil {
			t.Fatal(err)
		}
		if !isInsideWorktree(git.WorktreeInfo{Path: wtA, Branch: "feat-a"}) {
			t.Error("expected to be inside worktree")
		}
	})

	t.Run("inside different worktree", func(t *testing.T) {
		if err := os.Chdir(wtB); err != nil {
			t.Fatal(err)
		}
		if isInsideWorktree(git.WorktreeInfo{Path: wtA, Branch: "feat-a"}) {
			t.Error("expected not to be inside worktree feat-a while in feat-b")
		}
	})

	t.Run("at project root", func(t *testing.T) {
		if err := os.Chdir(tmp); err != nil {
			t.Fatal(err)
		}
		if isInsideWorktree(git.WorktreeInfo{Path: wtA, Branch: "feat-a"}) {
			t.Error("expected not to be inside worktree at project root")
		}
	})
}
