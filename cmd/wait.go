package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/termstatus"
	"github.com/bkildow/wtx/internal/ui"
	isatty "github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// Exit statuses of wtx wait besides 0 (ready) and 1 (wtx failed).
const (
	exitSetupFailed  = 2
	exitStillRunning = 75 // EX_TEMPFAIL: ask again
	exitInterrupted  = 130
)

const waitPollInterval = 500 * time.Millisecond

var errWaitTimedOut = errors.New("timed out waiting for setup")

func newWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "wait [name...]",
		Short: "Wait for background setup to finish",
		Long: fmt.Sprintf(`Wait until setup finishes in the named worktrees (default: the current one).

In a terminal that supports OSC 7501 the tab shows setup progress, then done
or error. Set WTX_TERMINAL_STATUS=off to disable it, or =all to also send
OSC 9;4 progress.

Exit status (the worst across worktrees; a failure beats still running):
  0    ready: setup complete, skipped, or none recorded
  %d    setup failed (run 'wtx logs <name>')
  %d   still running when --timeout ran out (run wtx wait again)
  %d  interrupted
  1    wtx failed`, exitSetupFailed, exitStillRunning, exitInterrupted),
		ValidArgsFunction: completeWorktreeNames,
		RunE:              runWait,
	}
	cmd.Flags().Bool("all", false, "Wait for every managed worktree")
	cmd.Flags().Duration("timeout", 10*time.Minute, "How long to wait; 0 checks once without waiting")
	cmd.Flags().Bool("json", false, "Write the final setup state as JSON to stdout")
	return cmd
}

// waitReport is the --json output of wtx wait. Adding fields is compatible;
// renaming or removing one needs a SchemaVersion bump.
type waitReport struct {
	SchemaVersion int         `json:"schema_version"`
	Worktrees     []waitEntry `json:"worktrees"`
}

// waitEntry is one worktree's final setup state, in the shape wtx status
// --json uses. Pending is true while a new worktree waits for setup to start.
type waitEntry struct {
	Branch  string              `json:"branch"`
	Path    string              `json:"path"`
	Setup   *project.SetupState `json:"setup"`
	Pending bool                `json:"pending,omitempty"`
}

func runWait(cmd *cobra.Command, args []string) error {
	all, _ := cmd.Flags().GetBool("all")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	structured, _ := cmd.Flags().GetBool("json")
	if all && len(args) > 0 {
		return fmt.Errorf("pass worktree names or --all, not both")
	}
	if timeout < 0 {
		return fmt.Errorf("--timeout must not be negative")
	}

	clone, err := openClone(cmd.Context())
	if err != nil {
		return err
	}
	managed, err := clone.ManagedWorktrees(cmd.Context())
	if err != nil {
		return err
	}
	selected, err := waitTargets(args, all, managed)
	if err != nil {
		// Exit 0 means ready, so an aborted picker exits as interrupted.
		if ui.IsUserAbort(err) {
			return exitError(exitInterrupted)
		}
		return err
	}
	if len(selected) == 0 {
		ui.Info("No worktrees found. Use 'wtx add' to create one.")
		return nil
	}

	cfg := clone.Config()
	expectSetup := len(cfg.Setup)+len(cfg.ParallelSetup) > 0
	targets := make([]project.SetupTarget, len(selected))
	for i, wt := range selected {
		targets[i] = project.SetupTarget{Branch: wt.Branch, Path: wt.Path, ExpectSetup: expectSetup}
	}

	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeoutCause(ctx, timeout, errWaitTimedOut)
	defer cancel()

	w := newSetupWatcher(targets, termstatus.Detect(os.Stderr, os.Getenv),
		!structured && isatty.IsTerminal(os.Stderr.Fd()))
	snaps := project.WatchSetup(ctx, targets, waitPollInterval, w.observe)
	code := waitExitCode(snaps, errors.Is(context.Cause(ctx), errWaitTimedOut))
	w.finish(snaps, code)

	if structured {
		report := waitReport{SchemaVersion: 1, Worktrees: make([]waitEntry, len(snaps))}
		for i, s := range snaps {
			reportCurrentLog(s.Path, s.State)
			report.Worktrees[i] = waitEntry{Branch: s.Branch, Path: s.Path, Setup: s.State, Pending: s.Pending}
		}
		if err := writeJSON(cmd.OutOrStdout(), report); err != nil {
			return err
		}
	} else {
		printWaitResult(snaps, code)
	}
	if code != 0 {
		return exitError(code)
	}
	return nil
}

// waitTargets resolves the worktrees to wait on: every managed worktree with
// --all, the named ones, the current one, or a picker in a terminal.
func waitTargets(args []string, all bool, managed []git.WorktreeInfo) ([]git.WorktreeInfo, error) {
	if all {
		return managed, nil
	}
	if len(args) == 0 {
		if wt, ok := resolveCurrentWorktree(managed); ok {
			return []git.WorktreeInfo{wt}, nil
		}
		if len(managed) == 0 {
			return nil, nil
		}
		if !isatty.IsTerminal(os.Stdin.Fd()) {
			return nil, fmt.Errorf("not inside a managed worktree: name a worktree or pass --all")
		}
		wt, err := selectWorktree(nil, managed)
		if err != nil {
			return nil, err
		}
		return []git.WorktreeInfo{wt}, nil
	}
	seen := map[string]bool{}
	var out []git.WorktreeInfo
	for _, arg := range args {
		wt, err := selectWorktree([]string{arg}, managed)
		if err != nil {
			return nil, err
		}
		if !seen[wt.Path] {
			seen[wt.Path] = true
			out = append(out, wt)
		}
	}
	return out, nil
}

// waitExitCode is the exit status for the final snapshots: a failure beats
// still running, which is a timeout or an interruption.
func waitExitCode(snaps []project.SetupSnapshot, timedOut bool) int {
	unsettled := false
	for _, s := range snaps {
		if s.State != nil && s.State.Status == project.SetupFailed {
			return exitSetupFailed
		}
		if !s.Settled() {
			unsettled = true
		}
	}
	switch {
	case !unsettled:
		return 0
	case timedOut:
		return exitStillRunning
	default:
		return exitInterrupted
	}
}

// setupWatcher reports setup progress while wtx wait polls: to the terminal
// as OSC 7501 records, and as one line per change when lines is set. One
// worktree uses the root record; several get a child record each, with the
// root as their rollup. It reports nothing when every setup was already
// settled on the first read.
type setupWatcher struct {
	term     *termstatus.Terminal
	lines    bool
	ids      map[string]string // worktree path -> child record id
	last     map[string]string // worktree path -> last line printed
	reported bool
}

func newSetupWatcher(targets []project.SetupTarget, term *termstatus.Terminal, lines bool) *setupWatcher {
	w := &setupWatcher{term: term, lines: lines, last: map[string]string{}}
	if len(targets) > 1 {
		w.ids = map[string]string{}
		for _, t := range targets {
			w.ids[t.Path] = termstatus.Slug(t.Branch, t.Path)
		}
	}
	return w
}

func (w *setupWatcher) observe(snaps []project.SetupSnapshot) {
	if !w.reported && project.SetupSnapshotsSettled(snaps) {
		return
	}
	w.reported = true
	for _, s := range snaps {
		if id, ok := w.ids[s.Path]; ok {
			w.term.Report(setupReport(id, s))
		}
	}
	w.term.Report(w.rootReport(snaps))
	if !w.lines {
		return
	}
	for _, s := range snaps {
		if s.Settled() {
			continue
		}
		if line := setupReport("", s).Msg; w.last[s.Path] != line {
			w.last[s.Path] = line
			ui.Step(line)
		}
	}
}

// rootReport is the root record: the single worktree, or the rollup.
func (w *setupWatcher) rootReport(snaps []project.SetupSnapshot) termstatus.Report {
	if w.ids == nil {
		return setupReport("", snaps[0])
	}
	return rollupReport(snaps)
}

// finish leaves the terminal with the outcome before wtx exits: the child
// records are cleared and the root says done or error, or idle when wait
// stopped watching while setup still runs.
func (w *setupWatcher) finish(snaps []project.SetupSnapshot, code int) {
	if !w.reported {
		return
	}
	for _, id := range w.ids {
		w.term.Report(termstatus.Report{ID: id, State: termstatus.Clear})
	}
	if code == exitStillRunning || code == exitInterrupted {
		w.term.Report(termstatus.Report{State: termstatus.Idle, Msg: "stopped waiting; setup is still running"})
		return
	}
	w.term.Report(w.rootReport(snaps))
}

// printWaitResult prints one line per worktree with how its setup ended.
func printWaitResult(snaps []project.SetupSnapshot, code int) {
	for _, s := range snaps {
		st := s.State
		switch {
		case !s.Settled():
			ui.Warning(setupReport("", s).Msg + " (still running)")
		case st == nil, st.Status == project.SetupSkipped:
			ui.Info(setupReport("", s).Msg)
		case st.Status == project.SetupFailed && !project.IsDirOrLinkToDir(s.Path):
			// Neither wtx logs nor wtx setup can help a removed worktree.
			ui.Warning(setupReport("", s).Msg)
		case st.Status == project.SetupFailed:
			ui.Warning(setupFailureHint(s.Branch, s.Path, st))
		case !st.StartedAt.IsZero() && !st.CompletedAt.IsZero():
			ui.Success(s.Branch + ": setup completed in " + ui.FormatDuration(st.CompletedAt.Sub(st.StartedAt)))
		default:
			ui.Success(s.Branch + " ready")
		}
	}
	if code == exitStillRunning {
		ui.Info("Run 'wtx wait' again to keep waiting.")
	}
}
