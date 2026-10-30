package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// TestClaudeHookWorktreeRemoveTeardownReadsEOF makes the process stdin a pipe
// that, like Claude Code's hook pipe, delivers the payload and never closes.
// Teardown hooks must read EOF rather than block on that pipe. Swapping
// os.Stdin is safe because no test in this package runs in parallel.
func TestClaudeHookWorktreeRemoveTeardownReadsEOF(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	feature := newSetupProject(t)
	root := filepath.Dir(filepath.Dir(feature))
	marker := filepath.Join(t.TempDir(), "teardown.txt")
	config := "git_dir: .git\nworktree_dir: worktrees\nshared_dir: shared\nteardown:\n  - read line; echo $? > " + marker + "\n"
	if err := os.WriteFile(filepath.Join(root, ".worktree.yml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = orig
		_ = w.Close()
		_ = r.Close()
	})
	payload := `{"hook_event_name":"WorktreeRemove","worktree_path":"` + feature + `","cwd":"` + root + `"}`
	if _, err := w.WriteString(payload); err != nil {
		t.Fatal(err)
	}

	out := captureUI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	if err := runClaudeHookWorktreeRemove(cmd, nil); err != nil {
		t.Fatalf("hook failed: %v\n%s", err, out.String())
	}

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("teardown did not finish: %v\n%s", err, out.String())
	}
	if got := strings.TrimSpace(string(data)); got == "0" {
		t.Error("teardown read a line from the hook pipe, want EOF")
	}
	if _, err := os.Stat(feature); !os.IsNotExist(err) {
		t.Errorf("worktree still exists: %v", err)
	}
}
