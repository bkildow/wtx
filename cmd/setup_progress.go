package cmd

import (
	"fmt"
	"strings"

	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/termstatus"
)

// setupReport is how one worktree's setup looks to the terminal. id "" is the
// root record, used when wtx reports on a single worktree; a child record
// carries the branch as its title.
func setupReport(id string, snap project.SetupSnapshot) termstatus.Report {
	r := termstatus.Report{ID: id}
	if id != "" {
		r.Title = snap.Branch
	}
	s := snap.State
	switch {
	case snap.Pending:
		r.State, r.Msg = termstatus.Working, snap.Branch+": waiting for setup to start"
	case s == nil:
		r.State, r.Msg = termstatus.Done, snap.Branch+": no setup recorded"
	case s.Status == project.SetupRunning && s.HooksTotal <= 0:
		r.State, r.Msg = termstatus.Working, snap.Branch+": setup running"
	case s.Status == project.SetupRunning:
		r.State, r.Msg = termstatus.Working, fmt.Sprintf("%s: hook %d/%d", snap.Branch, min(s.HooksCompleted+1, s.HooksTotal), s.HooksTotal)
		if pct, ok := s.Percent(); ok {
			r.Progress = termstatus.Percent(pct)
		}
	case s.Status == project.SetupFailed:
		reason, _, _ := strings.Cut(s.Error, "\n")
		r.State, r.Msg = termstatus.Error, snap.Branch+": setup failed: "+reason
	case s.Status == project.SetupSkipped:
		r.State, r.Msg = termstatus.Done, snap.Branch+": setup skipped"
	default:
		r.State, r.Msg = termstatus.Done, snap.Branch+" ready"
	}
	return r
}

// rollupReport combines several worktrees into the root record: an error if
// any setup failed, working while any is unsettled, otherwise done.
func rollupReport(snaps []project.SetupSnapshot) termstatus.Report {
	var failed []string
	ready, done, total := 0, 0, 0
	pending := false
	for _, s := range snaps {
		pending = pending || s.Pending
		switch {
		case s.State != nil && s.State.Status == project.SetupFailed:
			failed = append(failed, s.Branch)
		case s.Settled():
			ready++
		}
		if s.State != nil {
			done += s.State.HooksCompleted
			total += s.State.HooksTotal
		}
	}
	switch {
	case len(failed) == 1:
		return termstatus.Report{State: termstatus.Error, Msg: failed[0] + ": setup failed"}
	case len(failed) > 1:
		return termstatus.Report{State: termstatus.Error, Msg: fmt.Sprintf("setup failed in %d of %d worktrees", len(failed), len(snaps))}
	case ready < len(snaps):
		r := termstatus.Report{State: termstatus.Working, Msg: fmt.Sprintf("%d of %d worktrees ready", ready, len(snaps))}
		// A pending worktree's hook count is unknown until its setup starts,
		// so progress stays indeterminate rather than overstated.
		if total > 0 && !pending {
			r.Progress = termstatus.Percent(min(100*done/total, 99))
		}
		return r
	default:
		return termstatus.Report{State: termstatus.Done, Msg: fmt.Sprintf("%d worktrees ready", len(snaps))}
	}
}
