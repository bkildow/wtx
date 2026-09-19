package cmd

import (
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
)

// startSleeper launches a long-lived child and reaps it as soon as it exits.
// Without the reaper, IsProcessAlive (kill(pid, 0)) keeps reporting a zombie
// as running and the "did it actually die" assertion never flips.
func startSleeper(t *testing.T) int {
	t.Helper()

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start sleep: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})

	return cmd.Process.Pid
}

// TestTerminateBackgroundSetup guards the dry-run contract: `wt remove` reaches
// this path before teardown, and under --dry-run it must report the process it
// would signal rather than actually sending SIGTERM/SIGKILL.
func TestTerminateBackgroundSetup(t *testing.T) {
	ui.Output = io.Discard

	worktree := t.TempDir()
	pid := startSleeper(t)

	state := &project.SetupState{
		Status:    project.SetupRunning,
		PID:       pid,
		StartedAt: time.Now(),
	}
	if err := project.WriteSetupState(worktree, state); err != nil {
		t.Fatalf("could not write setup state: %v", err)
	}

	terminateBackgroundSetup(worktree, "feature-x", true)
	if !project.IsProcessAlive(pid) {
		t.Fatal("dry-run terminateBackgroundSetup killed the setup process; it must only report")
	}

	terminateBackgroundSetup(worktree, "feature-x", false)
	if project.IsProcessAlive(pid) {
		t.Error("terminateBackgroundSetup left the setup process running")
	}
}
