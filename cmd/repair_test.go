package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/git"
)

func TestWorktreeBareOverrideOK(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"missing file", "", false},
		{"simple false", "[core]\n\tbare = false\n", true},
		{"simple true", "[core]\n\tbare = true\n", false},
		{"missing key", "[core]\n\tfoo = bar\n", false},
		{"key in other section", "[remote \"x\"]\n\tbare = false\n", false},
		{"case-insensitive section", "[CORE]\n\tBARE = False\n", true},
		{"comments and blanks", "# comment\n\n[core]\n; another\nbare = false\n", true},
		{"boolean spelling", "[core]\n\tbare = no\n", true},
		{"last value wins", "[core]\nbare = false\n[core]\nbare = true\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wt := t.TempDir()
			if err := os.Mkdir(filepath.Join(wt, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			if tt.in != "" {
				if err := os.WriteFile(filepath.Join(wt, ".git", "config.worktree"), []byte(tt.in), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got, err := worktreeBareOverrideOK(context.Background(), wt)
			if err != nil || got != tt.want {
				t.Errorf("worktreeBareOverrideOK(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
			}
		})
	}
}

func TestWorktreeConfigPath_LinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Build a real bare repo + linked worktree so we can resolve a real .git
	// file pointer.
	srcDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", srcDir)
	run("-C", srcDir, "config", "user.email", "t@t")
	run("-C", srcDir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(srcDir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("-C", srcDir, "add", ".")
	run("-C", srcDir, "commit", "-m", "init")

	projectDir := t.TempDir()
	bareDir := filepath.Join(projectDir, ".bare")
	run("clone", "--bare", srcDir, bareDir)
	run("--git-dir", bareDir, "config", "extensions.worktreeConfig", "true")

	// Discover the default branch — git's clone --bare leaves HEAD pointing at it.
	headOut, err := exec.Command("git", "--git-dir", bareDir, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		t.Fatalf("symbolic-ref HEAD: %v", err)
	}
	branch := strings.TrimSpace(string(headOut))

	wtPath := filepath.Join(projectDir, "worktrees", branch)
	run("--git-dir", bareDir, "worktree", "add", "--relative-paths", wtPath, branch)

	got, err := git.WorktreeConfigPath(wtPath)
	if err != nil {
		t.Fatalf("worktreeConfigPath: %v", err)
	}
	want := filepath.Join(bareDir, "worktrees", branch, "config.worktree")
	if resolveForTest(got) != resolveForTest(want) {
		t.Errorf("worktreeConfigPath = %q, want %q", got, want)
	}
}

func resolveForTest(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}
