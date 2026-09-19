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

// Exercise actual executables: the in-process testscript entry point cannot
// verify the deprecated main's stderr or the shell's directory changes.
func TestRenameShellCompatibility(t *testing.T) {
	if testing.Short() {
		t.Skip("building binaries for shell integration")
	}
	bin := t.TempDir()
	for _, name := range []string{"wtx", "wt"} {
		c := exec.Command("go", "build", "-o", filepath.Join(bin, name), "../cmd/"+name)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", name, err, out)
		}
	}
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
		c.Env = gitEnv()
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

	env := append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"WTX_NO_DEPRECATION_WARN=", "NO_COLOR=1", "PROJECT="+project, "WORKTREE="+worktree)
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
	t.Run("shim output", func(t *testing.T) {
		out, stderr, err := run(t, project, filepath.Join(bin, "wt"), "cd", "main")
		if err != nil || strings.TrimSpace(out) != worktree || strings.Count(stderr, "wt is deprecated") != 1 {
			t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
		}
		out, stderr, err = run(t, project, filepath.Join(bin, "wtx"), "cd", "main")
		if err != nil || strings.TrimSpace(out) != worktree || stderr != "" {
			t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
		}
		out, stderr, err = run(t, project, filepath.Join(bin, "wt"), "not-a-command")
		if err == nil || out != "" || strings.Count(stderr, "wt is deprecated") != 1 {
			t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
		}
	})
	t.Run("warning opt out", func(t *testing.T) {
		c := exec.Command(filepath.Join(bin, "wt"), "root")
		c.Dir = project
		c.Env = append(append([]string(nil), env...), "WTX_NO_DEPRECATION_WARN=1")
		out, err := c.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != project {
			t.Fatalf("out=%q err=%v", out, err)
		}
	})
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, initBinary := range []string{"wt", "wtx"} {
			t.Run(shell+"/"+initBinary, func(t *testing.T) {
				shellPath, err := exec.LookPath(shell)
				if err != nil {
					t.Skipf("%s unavailable", shell)
				}
				script := `eval "$(command ` + initBinary + ` shell-init ` + shell + `)" || exit
wtx cd main || exit
[ "$PWD" = "$WORKTREE" ] || exit 21
wt root || exit
[ "$PWD" = "$PROJECT" ] || exit 22
wt cd main || exit
[ "$PWD" = "$WORKTREE" ] || exit 23
wtx root || exit
[ "$PWD" = "$PROJECT" ] || exit 24
wtx cd missing && exit 25
wt cd missing && exit 26
exit 0
`
				args := []string{"--noprofile", "--norc", "-c"}
				if shell == "zsh" {
					args = []string{"-f", "-c"}
					script = "autoload -Uz compinit; compinit -D\n" + script
				}
				if shell == "fish" {
					args = []string{"--no-config", "-c"}
					script = `command ` + initBinary + ` shell-init fish | source
wtx cd main; or exit
 test "$PWD" = "$WORKTREE"; or exit 21
wt root; or exit
 test "$PWD" = "$PROJECT"; or exit 22
wt cd main; or exit
 test "$PWD" = "$WORKTREE"; or exit 23
wtx root; or exit
 test "$PWD" = "$PROJECT"; or exit 24
wtx cd missing; and exit 25
wt cd missing; and exit 26
exit 0
`
				}
				out, stderr, err := run(t, project, shellPath, append(args, script)...)
				if err != nil || out != "" {
					t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
				}
				warnings := 3
				if initBinary == "wt" {
					warnings++
				}
				if strings.Count(stderr, "wt is deprecated") != warnings {
					t.Fatalf("expected %d warnings, got %q", warnings, stderr)
				}
			})
		}
	}
}
