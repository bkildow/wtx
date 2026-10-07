package git

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIntegrationCommonDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	ctx := context.Background()

	t.Run("not a repo", func(t *testing.T) {
		_, err := CommonDir(ctx, t.TempDir())
		if !errors.Is(err, ErrNotGitRepo) {
			t.Fatalf("err = %v, want ErrNotGitRepo", err)
		}
	})

	t.Run("normal repo", func(t *testing.T) {
		repo := t.TempDir()
		if out, err := exec.Command("git", "init", repo).CombinedOutput(); err != nil {
			t.Fatalf("git init: %v\n%s", err, out)
		}
		got, err := CommonDir(ctx, repo)
		if err != nil {
			t.Fatalf("CommonDir: %v", err)
		}
		want, _ := filepath.EvalSymlinks(filepath.Join(repo, ".git"))
		if resolved, _ := filepath.EvalSymlinks(got); resolved != want {
			t.Errorf("CommonDir = %q, want %q", got, want)
		}
	})
}
