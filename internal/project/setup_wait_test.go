package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newWaitWorktree(t *testing.T, age time.Duration) string {
	t.Helper()
	dir := t.TempDir()
	gitFile := filepath.Join(dir, ".git")
	if err := os.WriteFile(gitFile, []byte("gitdir: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(gitFile, when, when); err != nil {
		t.Fatal(err)
	}
	return dir
}

func running() *SetupState {
	return &SetupState{Status: SetupRunning, PID: os.Getpid(), HooksTotal: 4, HooksCompleted: 1}
}

func TestWatchSetupSettledReturnsAfterOneRead(t *testing.T) {
	dir := newWaitWorktree(t, time.Hour)
	if err := WriteSetupState(dir, &SetupState{Status: SetupComplete}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	snaps := WatchSetup(context.Background(), []SetupTarget{{Branch: "a", Path: dir}}, time.Millisecond, func([]SetupSnapshot) { calls++ })
	if calls != 1 || snaps[0].State.Status != SetupComplete {
		t.Errorf("calls = %d, status = %v; want 1 call and complete", calls, snaps[0].State)
	}
}

func TestWatchSetupFollowsProgressToCompletion(t *testing.T) {
	dir := newWaitWorktree(t, time.Hour)
	state := running()
	if err := WriteSetupState(dir, state); err != nil {
		t.Fatal(err)
	}

	var seen []int
	observe := func(snaps []SetupSnapshot) {
		seen = append(seen, snaps[0].State.HooksCompleted)
		switch snaps[0].State.HooksCompleted {
		case 1:
			state.HooksCompleted = 3
		case 3:
			state.HooksCompleted = 4
			state.Status = SetupComplete
		}
		if err := WriteSetupState(dir, state); err != nil {
			t.Error(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snaps := WatchSetup(ctx, []SetupTarget{{Branch: "a", Path: dir}}, time.Millisecond, observe)
	if snaps[0].State.Status != SetupComplete {
		t.Errorf("final status = %s, want complete", snaps[0].State.Status)
	}
	if len(seen) != 3 || seen[0] != 1 || seen[1] != 3 || seen[2] != 4 {
		t.Errorf("observed hooks %v, want [1 3 4] (one call per change)", seen)
	}
}

func TestWatchSetupExpiredContextChecksOnce(t *testing.T) {
	dir := newWaitWorktree(t, time.Hour)
	if err := WriteSetupState(dir, running()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	snaps := WatchSetup(ctx, []SetupTarget{{Branch: "a", Path: dir}}, time.Hour, func([]SetupSnapshot) { calls++ })
	if calls != 1 || snaps[0].State.Status != SetupRunning {
		t.Errorf("calls = %d, status = %s; want one read showing running", calls, snaps[0].State.Status)
	}
}

func TestWatchSetupFailures(t *testing.T) {
	t.Run("state disappears while running", func(t *testing.T) {
		dir := newWaitWorktree(t, time.Hour)
		if err := WriteSetupState(dir, running()); err != nil {
			t.Fatal(err)
		}
		snaps := WatchSetup(context.Background(), []SetupTarget{{Path: dir}}, time.Millisecond, func([]SetupSnapshot) {
			_ = os.Remove(SetupStatePath(dir))
		})
		if got := snaps[0].State; got == nil || got.Status != SetupFailed || got.Error != "setup state disappeared" {
			t.Errorf("state = %+v, want failed: setup state disappeared", got)
		}
	})
	t.Run("unreadable state", func(t *testing.T) {
		dir := newWaitWorktree(t, time.Hour)
		if err := os.WriteFile(SetupStatePath(dir), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		snaps := WatchSetup(context.Background(), []SetupTarget{{Path: dir}}, time.Millisecond, func([]SetupSnapshot) {})
		if got := snaps[0].State; got == nil || got.Status != SetupFailed || !strings.HasPrefix(got.Error, "cannot read setup state: ") {
			t.Errorf("state = %+v, want failed: cannot read setup state", got)
		}
	})
	t.Run("worktree removed", func(t *testing.T) {
		dir := newWaitWorktree(t, time.Hour)
		if err := WriteSetupState(dir, running()); err != nil {
			t.Fatal(err)
		}
		snaps := WatchSetup(context.Background(), []SetupTarget{{Path: dir}}, time.Millisecond, func([]SetupSnapshot) {
			_ = os.RemoveAll(dir)
		})
		if got := snaps[0].State; got == nil || got.Status != SetupFailed || got.Error != "worktree was removed" {
			t.Errorf("state = %+v, want failed: worktree was removed", got)
		}
	})
}

func TestReadSetupSnapshotPending(t *testing.T) {
	tests := []struct {
		name   string
		age    time.Duration
		expect bool
		want   bool
	}{
		{"young worktree with hooks waits for setup to start", time.Second, true, true},
		{"old worktree with hooks has no setup recorded", 2 * SetupStartGrace, true, false},
		{"young worktree without hooks is settled", time.Second, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newWaitWorktree(t, tt.age)
			snap := readSetupSnapshot(SetupTarget{Path: dir, ExpectSetup: tt.expect}, nil)
			if snap.Pending != tt.want || snap.State != nil {
				t.Errorf("Pending = %v, State = %v; want Pending %v and no state", snap.Pending, snap.State, tt.want)
			}
		})
	}
}

func TestSetupStatePercent(t *testing.T) {
	tests := []struct {
		state *SetupState
		pct   int
		ok    bool
	}{
		{nil, 0, false},
		{&SetupState{Status: SetupRunning}, 0, false},
		{&SetupState{Status: SetupRunning, HooksTotal: 4, HooksCompleted: 1}, 25, true},
		{&SetupState{Status: SetupRunning, HooksTotal: 2, HooksCompleted: 2}, 99, true},
		{&SetupState{Status: SetupComplete, HooksTotal: 2, HooksCompleted: 2}, 100, true},
	}
	for _, tt := range tests {
		pct, ok := tt.state.Percent()
		if pct != tt.pct || ok != tt.ok {
			t.Errorf("Percent(%+v) = %d, %v; want %d, %v", tt.state, pct, ok, tt.pct, tt.ok)
		}
	}
}
