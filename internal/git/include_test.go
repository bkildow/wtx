package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseNULList(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []string
	}{
		{"empty", "", nil},
		{"single", ".env\x00", []string{".env"}},
		{"multiple with spaces", ".env\x00secrets/my key.txt\x00", []string{".env", "secrets/my key.txt"}},
		{"no trailing NUL", "a\x00b", []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseNULList(tt.output); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseNULList(%q) = %v, want %v", tt.output, got, tt.want)
			}
		})
	}
}

func TestListWorktreeIncludesMissingFile(t *testing.T) {
	got, err := ListWorktreeIncludes(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func mustRun(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cmd %v failed: %v\n%s", args, err, out)
	}
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// seedIncludeRepo creates a source repo whose committed content exercises
// every .worktreeinclude rule, and returns its path.
func seedIncludeRepo(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	mustRun(t, "git", "init", "-q", "-b", "main", src)
	writeFiles(t, src, map[string]string{
		".gitignore":       ".env\nsecrets/\nbuild/\n",
		".worktreeinclude": ".env*\nsecrets/\n",
		// Tracked and matching a pattern: must never be copied.
		".env.example": "EXAMPLE=1\n",
	})
	mustRun(t, "git", "-C", src, "add", ".")
	// Tracked despite being ignored, and matching a pattern.
	writeFiles(t, src, map[string]string{"secrets/tracked.txt": "tracked\n"})
	mustRun(t, "git", "-C", src, "add", "-f", "secrets/tracked.txt")
	mustRun(t, "git", "-C", src, "commit", "-q", "-m", "initial")
	return src
}

// addLocalFiles creates the machine-local files a developer would have in
// their main checkout.
func addLocalFiles(t *testing.T, root string) {
	t.Helper()
	writeFiles(t, root, map[string]string{
		".env":            "SECRET=1\n", // ignored + listed: copied
		"secrets/key.txt": "key\n",      // ignored + listed: copied
		"build/out.txt":   "out\n",      // ignored, not listed: skipped
		".envrc":          "export X\n", // listed, not ignored: skipped
	})
}

func TestIntegrationListWorktreeIncludes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	want := []string{".env", "secrets/key.txt"}

	t.Run("init layout", func(t *testing.T) {
		src := seedIncludeRepo(t)
		addLocalFiles(t, src)

		got, err := ListWorktreeIncludes(context.Background(), src)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("bare layout main worktree", func(t *testing.T) {
		src := seedIncludeRepo(t)
		projectDir := t.TempDir()
		bare := filepath.Join(projectDir, ".bare")
		mainWT := filepath.Join(projectDir, "main")
		mustRun(t, "git", "clone", "-q", "--bare", src, bare)
		mustRun(t, "git", "--git-dir", bare, "worktree", "add", "-q", mainWT, "main")
		addLocalFiles(t, mainWT)

		// The linked worktree has its own index; reading it through the bare
		// git dir would report the tracked files as untracked.
		got, err := ListWorktreeIncludes(context.Background(), mainWT)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}
