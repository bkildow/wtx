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

	// Ignored files are what make removal slow in practice (node_modules,
	// build output), and git deletes them along with the worktree.
	r.commit(".gitignore", "ignored/\n")

	root := t.TempDir()
	var paths []string
	for i := range 8 {
		path := filepath.Join(root, fmt.Sprintf("wt-%d", i))
		r.git("worktree", "add", "-q", "-b", fmt.Sprintf("branch-%d", i), path)
		writeFiles(t, path, map[string]string{"ignored/a": "x", "ignored/b": "x"})
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
