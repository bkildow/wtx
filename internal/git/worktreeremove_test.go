package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestWorktreeRemoveConcurrently pins what prune relies on: git worktree
// remove takes no repository-wide lock, so distinct worktrees can be removed
// at the same time.
func TestWorktreeRemoveConcurrently(t *testing.T) {
	r := newMergeRepo(t)
	runner := r.runner()
	ctx := context.Background()

	root := t.TempDir()
	var paths []string
	for i := range 8 {
		path := filepath.Join(root, fmt.Sprintf("wt-%d", i))
		r.git("worktree", "add", "-q", "-b", fmt.Sprintf("branch-%d", i), path)
		if err := os.WriteFile(filepath.Join(path, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r.git("-C", path, "add", ".gitignore")
		r.git("-C", path, "commit", "-qm", "ignore")
		// Ignored files are what make removal slow in practice (node_modules,
		// build output), and git deletes them along with the worktree.
		if err := os.MkdirAll(filepath.Join(path, "ignored"), 0o755); err != nil {
			t.Fatal(err)
		}
		for j := range 50 {
			if err := os.WriteFile(filepath.Join(path, "ignored", fmt.Sprintf("f%d", j)), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		paths = append(paths, path)
	}

	var wg sync.WaitGroup
	errs := make([]error, len(paths))
	for i, path := range paths {
		wg.Go(func() {
			errs[i] = runner.WorktreeRemove(ctx, path, false)
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("WorktreeRemove(%s): %v", paths[i], err)
		}
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s still exists after removal (stat err: %v)", path, err)
		}
	}

	wts, err := runner.WorktreeList(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(wts) != 1 {
		t.Errorf("worktree list has %d entries after removal, want only the main one: %+v", len(wts), wts)
	}
}
