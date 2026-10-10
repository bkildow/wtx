package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// spyGit puts a git wrapper first on PATH that logs each invocation's
// arguments, one line per call, and returns a func counting the
// `git worktree list` calls.
func spyGit(t *testing.T) func() int {
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
	return func() int {
		data, _ := os.ReadFile(log)
		return strings.Count(string(data), "worktree list")
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
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	tests := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
		// reports means the command fails on this fixture's findings.
		reports bool
	}{
		{name: "run", cmd: newRunCmd, args: []string{"noop"}},
		{name: "apply", cmd: newApplyCmd, args: []string{"feature"}},
		{name: "apply --all", cmd: func() *cobra.Command {
			c := newApplyCmd()
			_ = c.Flags().Set("all", "true")
			return c
		}},
		{name: "repair", cmd: newRepairCmd},
		{name: "doctor", cmd: newDoctorCmd, reports: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			listingProject(t)
			count := spyGit(t)
			command := tt.cmd()
			command.SetContext(context.Background())
			if err := command.RunE(command, tt.args); err != nil && !tt.reports {
				t.Fatal(err)
			}
			if n := count(); n != 1 {
				t.Errorf("git worktree list ran %d times, want 1", n)
			}
		})
	}
}
