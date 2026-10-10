package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// spyGit puts a git wrapper first on PATH that logs each invocation's
// arguments, one line per call, and returns a func counting the calls that
// contain all of words.
func spyGit(t *testing.T) func(words ...string) int {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "git.log")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\nexec '" + gitPath + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func(words ...string) int {
		data, _ := os.ReadFile(log)
		n := 0
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 0 && !slices.ContainsFunc(words, func(w string) bool { return !slices.Contains(fields, w) }) {
				n++
			}
		}
		return n
	}
}

// listingProject builds a Bare layout project with a Main worktree, a
// feature worktree and a run script, and changes into the feature worktree.
func listingProject(t *testing.T) {
	t.Helper()
	t.Setenv("WTX_HOME", t.TempDir())
	base := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	src := filepath.Join(base, "src")
	runGit("init", "-b", "main", src)
	runGit("-C", src, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "initial")
	root := filepath.Join(base, "proj")
	gitDir := filepath.Join(root, ".bare")
	runGit("clone", "--bare", src, gitDir)
	config := "version: 1\ngit_dir: .bare\nworktree_dir: worktrees\nshared_dir: shared\nscripts:\n  noop: noop.sh\n"
	if err := os.WriteFile(filepath.Join(root, ".worktree.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "noop.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit("--git-dir", gitDir, "worktree", "add", filepath.Join(root, "worktrees", "main"), "main")
	feature := filepath.Join(root, "worktrees", "feature")
	runGit("--git-dir", gitDir, "worktree", "add", "-b", "feature", feature)
	if err := os.WriteFile(filepath.Join(root, "worktrees", "main", ".worktreeinclude"), []byte(".env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(feature)
}

func TestCommandsListWorktreesOnce(t *testing.T) {
	tests := []struct {
		name string
		cmd  func() *cobra.Command
		run  func(*cobra.Command, []string) error
		args []string
	}{
		{"run", newRunCmd, runRun, []string{"noop"}},
		{"apply", newApplyCmd, runApply, []string{"feature"}},
		{"apply --all", func() *cobra.Command {
			c := newApplyCmd()
			_ = c.Flags().Set("all", "true")
			return c
		}, runApply, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listingProject(t)
			count := spyGit(t)
			command := tt.cmd()
			command.SetContext(context.Background())
			if err := tt.run(command, tt.args); err != nil {
				t.Fatal(err)
			}
			if n := count("worktree", "list"); n != 1 {
				t.Errorf("git worktree list ran %d times, want 1", n)
			}
		})
	}
}
