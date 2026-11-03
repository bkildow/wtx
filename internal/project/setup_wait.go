package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// SetupStartGrace is how long a new worktree with setup hooks may go without
// a setup state before waiting gives up on one appearing. herdr's
// worktree.created handler starts setup after the checkout already exists.
const SetupStartGrace = 60 * time.Second

// SetupTarget is a worktree to wait on.
type SetupTarget struct {
	Branch string
	Path   string
	// ExpectSetup is true when the project has setup hooks, so a young
	// worktree without a state is waiting for setup to start.
	ExpectSetup bool
}

// SetupSnapshot is a target's latest setup state. State is nil when no setup
// is recorded. Pending is true while a young worktree waits for its setup to
// start. A removed worktree or unreadable state becomes a failed State.
type SetupSnapshot struct {
	SetupTarget
	State   *SetupState
	Pending bool
}

// Settled reports whether waiting on this worktree can end.
func (s SetupSnapshot) Settled() bool {
	return !s.Pending && s.State.Settled()
}

// WatchSetup reads every target, calls observe, then polls every interval
// until every snapshot is settled or ctx ends, calling observe again only when
// a snapshot changed. It returns the last snapshots; any still unsettled mean
// ctx ended first. A ctx that has already ended still gets one read, which is
// how a caller checks once without waiting. It only reads: dead setup
// processes are resolved in memory, and it runs no git commands.
func WatchSetup(ctx context.Context, targets []SetupTarget, interval time.Duration, observe func([]SetupSnapshot)) []SetupSnapshot {
	snaps := make([]SetupSnapshot, len(targets))
	for i, t := range targets {
		snaps[i] = readSetupSnapshot(t, nil)
	}
	observe(append([]SetupSnapshot(nil), snaps...))

	tick := time.NewTicker(interval)
	defer tick.Stop()
	for !SetupSnapshotsSettled(snaps) {
		select {
		case <-ctx.Done():
			return snaps
		case <-tick.C:
		}
		changed := false
		for i, prev := range snaps {
			if prev.Settled() {
				continue
			}
			next := readSetupSnapshot(prev.SetupTarget, prev.State)
			if snapshotChanged(prev, next) {
				changed = true
			}
			snaps[i] = next
		}
		if changed {
			observe(append([]SetupSnapshot(nil), snaps...))
		}
	}
	return snaps
}

// SetupSnapshotsSettled reports whether every snapshot is settled.
func SetupSnapshotsSettled(snaps []SetupSnapshot) bool {
	for _, s := range snaps {
		if !s.Settled() {
			return false
		}
	}
	return true
}

// readSetupSnapshot reads one target. prev is the state seen on the last
// read, so a running state that disappears is reported as failed.
func readSetupSnapshot(t SetupTarget, prev *SetupState) SetupSnapshot {
	snap := SetupSnapshot{SetupTarget: t}
	state := ObserveSetupState(t.Path)
	switch {
	case state == nil && !exists(t.Path):
		snap.State = failedSetupState("worktree was removed")
	case state == nil && prev != nil && prev.Status == SetupRunning:
		snap.State = failedSetupState("setup state disappeared")
	case state == nil:
		snap.Pending = t.ExpectSetup && worktreeAge(t.Path) < SetupStartGrace
	default:
		snap.State = state
	}
	return snap
}

// worktreeAge is how long ago git created the worktree's .git file. An
// unreadable .git counts as old, so waiting never hangs on it.
func worktreeAge(path string) time.Duration {
	info, err := os.Stat(filepath.Join(path, ".git"))
	if err != nil {
		return SetupStartGrace
	}
	return time.Since(info.ModTime())
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, os.ErrNotExist)
}

func failedSetupState(reason string) *SetupState {
	return &SetupState{Status: SetupFailed, Error: reason, CompletedAt: time.Now()}
}

func snapshotChanged(a, b SetupSnapshot) bool {
	if a.Pending != b.Pending || (a.State == nil) != (b.State == nil) {
		return true
	}
	if a.State == nil {
		return false
	}
	return a.State.Status != b.State.Status ||
		a.State.HooksCompleted != b.State.HooksCompleted ||
		a.State.Error != b.State.Error
}
