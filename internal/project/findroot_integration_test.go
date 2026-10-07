package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bkildow/wtx/internal/config"
)

func requireGit(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func runGit(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// initRepo creates a normal repo at dir with one commit on main. When
// commitConfig is true, a .worktree.yml is part of that commit.
func initRepo(t *testing.T, dir string, commitConfig bool) {
	t.Helper()
	runGit(t, "init", "-b", "main", dir)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if commitConfig {
		if err := saveConfig(dir); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, "-C", dir, "add", ".")
	runGit(t, "-C", dir, "commit", "-m", "initial")
}

func saveConfig(dir string) error {
	cfg := config.DefaultConfig()
	return cfg.Save(dir)
}

func assertSameDir(t *testing.T, got, want string) {
	t.Helper()
	g, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", got, err)
	}
	w, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", want, err)
	}
	if g != w {
		t.Errorf("FindRoot = %q, want %q", got, want)
	}
}

func TestFindRootFromExternalWorktreeBareLayout(t *testing.T) {
	requireGit(t)
	for _, tc := range []struct {
		name         string
		commitConfig bool
	}{
		{"without committed config", false},
		{"with committed config", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := t.TempDir()
			initRepo(t, src, tc.commitConfig)

			root := t.TempDir()
			bare := filepath.Join(root, ".bare")
			runGit(t, "clone", "--bare", src, bare)
			if err := saveConfig(root); err != nil {
				t.Fatal(err)
			}

			wt := filepath.Join(t.TempDir(), "feature")
			runGit(t, "--git-dir", bare, "worktree", "add", "-b", "feature", wt, "main")
			sub := filepath.Join(wt, "nested")
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatal(err)
			}

			for _, start := range []string{wt, sub} {
				got, err := FindRoot(start)
				if err != nil {
					t.Fatalf("FindRoot(%q): %v", start, err)
				}
				assertSameDir(t, got, root)
			}
		})
	}
}

func TestFindRootFromExternalWorktreeGitLayout(t *testing.T) {
	requireGit(t)
	for _, tc := range []struct {
		name         string
		commitConfig bool
	}{
		{"without committed config", false},
		{"with committed config", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			initRepo(t, root, tc.commitConfig)
			if !tc.commitConfig {
				// Untracked config at the root only.
				if err := saveConfig(root); err != nil {
					t.Fatal(err)
				}
			}

			wt := filepath.Join(t.TempDir(), "feature")
			runGit(t, "-C", root, "worktree", "add", "-b", "feature", wt, "main")

			got, err := FindRoot(wt)
			if err != nil {
				t.Fatalf("FindRoot: %v", err)
			}
			assertSameDir(t, got, root)
		})
	}
}

func TestFindRootInsideRootKeepsCallerSpelling(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	initRepo(t, root, true)
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := FindRoot(sub)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	// Same string as the caller's path, not git's symlink-resolved form.
	if got != root {
		t.Errorf("FindRoot = %q, want %q", got, root)
	}
}

func TestFindRootGitRepoWithoutRootConfigFallsBack(t *testing.T) {
	requireGit(t)
	repo := t.TempDir()
	initRepo(t, repo, false)
	// Config lives below the repo root, not next to .git: only the
	// walk-up can find it.
	proj := filepath.Join(repo, "proj")
	nested := filepath.Join(proj, "x")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(proj); err != nil {
		t.Fatal(err)
	}

	got, err := FindRoot(nested)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	if got != proj {
		t.Errorf("FindRoot = %q, want %q", got, proj)
	}
}

func TestFindRootNonGitDirFallsBack(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := saveConfig(root); err != nil {
		t.Fatal(err)
	}

	got, err := FindRoot(nested)
	if err != nil {
		t.Fatalf("FindRoot: %v", err)
	}
	if got != root {
		t.Errorf("FindRoot = %q, want %q", got, root)
	}
}
