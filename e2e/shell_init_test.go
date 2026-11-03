package e2e_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bkildow/wtx/internal/config"
)

// Exercise the actual executable: the in-process testscript entry point cannot
// verify the shell's directory changes.
func TestShellInitChangesDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("building binaries for shell integration")
	}
	bin := buildWTX(t)
	project := filepath.Join(t.TempDir(), "project with spaces")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	// Git and os.Getwd resolve the macOS /var symlink. Compare canonical paths.
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	if err := cfg.Save(project); err != nil {
		t.Fatal(err)
	}
	gitRun := func(args ...string) {
		c := exec.Command("git", args...)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	seed := t.TempDir()
	gitRun("init", "-b", "main", seed)
	gitRun("-C", seed, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "initial")
	bare := filepath.Join(project, ".bare")
	gitRun("clone", "--bare", seed, bare)
	worktree := filepath.Join(project, "worktrees", "main")
	gitRun("--git-dir", bare, "worktree", "add", worktree, "main")

	// CI may have writable completion directories in fpath. Include one to
	// ensure non-interactive initialization never tries to prompt about it.
	completionDir := filepath.Join(bin, "insecure-completions")
	if err := os.Mkdir(completionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(completionDir, "_probe"), []byte("#compdef probe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(completionDir, 0o777); err != nil { // #nosec G302 -- intentionally insecure test fixture inside t.TempDir.
		t.Fatal(err)
	}

	env := append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"NO_COLOR=1", "PROJECT="+project, "WORKTREE="+worktree, "TEST_COMPLETION_DIR="+completionDir)
	run := func(t *testing.T, dir, program string, args ...string) (string, string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, program, args...)
		c.Dir, c.Env = dir, env
		var out, stderr bytes.Buffer
		c.Stdout, c.Stderr = &out, &stderr
		err := c.Run()
		return out.String(), stderr.String(), err
	}
	t.Run("cd prints the path", func(t *testing.T) {
		out, stderr, err := run(t, project, filepath.Join(bin, "wtx"), "cd", "main")
		if err != nil || strings.TrimSpace(out) != worktree || stderr != "" {
			t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
		}
	})
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			shellPath, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s unavailable", shell)
			}
			script := `eval "$(command wtx shell-init ` + shell + `)" || exit
wtx cd main || exit
[ "$PWD" = "$WORKTREE" ] || exit 21
wtx root || exit
[ "$PWD" = "$PROJECT" ] || exit 22
wtx cd missing && exit 23
exit 0
`
			args := []string{"--noprofile", "--norc", "-c"}
			if shell == "zsh" {
				args = []string{"-f", "-c"}
				script = "fpath=(\"$TEST_COMPLETION_DIR\" $fpath); autoload -Uz compinit; compinit -i -D || exit\n" + script
			}
			if shell == "fish" {
				args = []string{"--no-config", "-c"}
				script = `command wtx shell-init fish | source
wtx cd main; or exit
test "$PWD" = "$WORKTREE"; or exit 21
wtx root; or exit
test "$PWD" = "$PROJECT"; or exit 22
wtx cd missing; and exit 23
exit 0
`
			}
			out, stderr, err := run(t, project, shellPath, append(args, script)...)
			if err != nil || out != "" {
				t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
			}
		})
	}
}

// buildWTX builds the wtx binary into a new directory and returns it, for
// tests that run the real executable instead of the testscript entry point.
func buildWTX(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "wtx"), "../cmd/wtx").CombinedOutput(); err != nil {
		t.Fatalf("build wtx: %v\n%s", err, out)
	}
	return bin
}
