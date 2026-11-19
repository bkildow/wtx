package e2e_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/project"
)

// TestBackgroundSetupRecordsOutcome runs background setup through the real
// binary, which re-executes itself as wtx _run-setup, and checks the record
// ends complete: the launching wtx's start record must never land on top of
// the child's outcome, however fast its hooks finish.
func TestBackgroundSetupRecordsOutcome(t *testing.T) {
	if testing.Short() {
		t.Skip("real binary integration test")
	}
	bin := buildWTX(t)
	root, wtxHome := realProject(t, bin)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Setup = []string{"touch setup-marker"}
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	wtx := func(args ...string) string {
		t.Helper()
		c := exec.Command(filepath.Join(bin, "wtx"), args...)
		c.Dir = root
		c.Env = realEnv(wtxHome)
		out, err := c.Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				t.Fatalf("wtx %v: %v: %s", args, err, exit.Stderr)
			}
			t.Fatalf("wtx %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}

	lines := strings.Split(wtx("add", "--background", "feature"), "\n")
	worktree := lines[len(lines)-1]
	wtx("wait", "feature")

	state, err := project.ReadSetupState(worktree)
	if err != nil || state == nil {
		t.Fatalf("ReadSetupState = %v, %v", state, err)
	}
	if state.Status != project.SetupComplete || state.HooksCompleted != 1 || !state.Background() {
		t.Fatalf("setup record = %+v, want a complete background run of 1 hook", state)
	}
	if _, err := os.Stat(filepath.Join(worktree, "setup-marker")); err != nil {
		t.Fatalf("setup hook did not run: %v", err)
	}
	if project.SetupLog(worktree, state) == "" {
		t.Fatal("background setup left no log")
	}
}
