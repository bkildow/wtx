package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const (
	SetupStateFile = ".wtx-setup.json"
	SetupLogFile   = ".wtx-setup.log"

	// staleProcessError is the failure recorded for a running setup whose
	// process is no longer alive.
	staleProcessError = "setup process exited unexpectedly"
)

// SetupStatus is where a setup run is in its lifecycle.
//
// A SetupState records one run. It starts running or skipped and only moves
// along these edges:
//
//	(new run) ── start ──▶ running ── progress ──▶ running
//	(new run) ── skip ───▶ skipped
//	running ── finish(nil) ──▶ complete
//	running ── finish(err), fail ──▶ failed
//
// complete, failed and skipped are settled and never change; running is the
// only unsettled status. Running setup again (wtx setup) starts a new record
// that replaces a settled one. A running record belongs to a foreground run
// (no log; see Background) or a background run, which logs to SetupLogFile.
type SetupStatus string

const (
	SetupRunning  SetupStatus = "running"
	SetupComplete SetupStatus = "complete"
	SetupFailed   SetupStatus = "failed"
	SetupSkipped  SetupStatus = "skipped"
)

// setupTransitions lists the statuses each status may move to. Settled
// statuses have no entry.
var setupTransitions = map[SetupStatus][]SetupStatus{
	SetupRunning: {SetupRunning, SetupComplete, SetupFailed},
}

// SetupState tracks the progress of setup hooks for a worktree. Change it
// only through its lifecycle methods, so every change follows SetupStatus.
type SetupState struct {
	Status         SetupStatus `json:"status"`
	PID            int         `json:"pid"`
	StartedAt      time.Time   `json:"started_at"`
	CompletedAt    time.Time   `json:"completed_at,omitzero"`
	HooksTotal     int         `json:"hooks_total"`
	HooksCompleted int         `json:"hooks_completed"`
	Error          string      `json:"error,omitempty"`
	LogFile        string      `json:"log_file"`
}

// StartForegroundSetup starts a run of hooksTotal hooks in process pid that
// reports to its terminal and keeps no log.
func StartForegroundSetup(pid, hooksTotal int) *SetupState {
	return &SetupState{Status: SetupRunning, PID: pid, StartedAt: time.Now(), HooksTotal: hooksTotal}
}

// StartBackgroundSetup starts a run of hooksTotal hooks in the detached
// process pid, which logs to the worktree's setup log.
func StartBackgroundSetup(worktreePath string, pid, hooksTotal int) *SetupState {
	s := StartForegroundSetup(pid, hooksTotal)
	s.LogFile = SetupLogPath(worktreePath)
	return s
}

// SkipSetup records a worktree whose setup hooks were deliberately not run.
func SkipSetup() *SetupState {
	now := time.Now()
	return &SetupState{Status: SetupSkipped, StartedAt: now, CompletedAt: now}
}

// failedObservation is the failed state reported for a worktree whose setup
// went wrong outside any run's record (the record is unreadable or gone).
// at is when it was seen, or zero when the record does not say.
func failedObservation(reason string, at time.Time) *SetupState {
	return &SetupState{Status: SetupFailed, Error: reason, CompletedAt: at}
}

// to moves s to next when its lifecycle allows it, reporting whether it did.
func (s *SetupState) to(next SetupStatus) bool {
	if !slices.Contains(setupTransitions[s.Status], next) {
		return false
	}
	s.Status = next
	return true
}

// Progress records that completed hooks of a running setup have finished.
func (s *SetupState) Progress(completed int) {
	if s.to(SetupRunning) {
		s.HooksCompleted = completed
	}
}

// Finish ends a running setup once every hook has run: complete when err is
// nil, else failed with err.
func (s *SetupState) Finish(err error) {
	next := SetupComplete
	if err != nil {
		next = SetupFailed
	}
	if !s.to(next) {
		return
	}
	s.HooksCompleted = s.HooksTotal
	s.CompletedAt = time.Now()
	if err != nil {
		s.Error = err.Error()
	}
}

// Fail ends a running setup that stopped before its hooks finished,
// reporting whether it was running.
func (s *SetupState) Fail(reason string) bool {
	if !s.to(SetupFailed) {
		return false
	}
	s.Error = reason
	s.CompletedAt = time.Now()
	return true
}

// Settled reports whether waiting on this setup can end: only running is
// unsettled. A nil state means no setup was recorded, which is settled.
func (s *SetupState) Settled() bool {
	return s == nil || len(setupTransitions[s.Status]) == 0
}

// Failed reports whether the setup ended in failure. A nil state has not.
func (s *SetupState) Failed() bool {
	return s != nil && s.Status == SetupFailed
}

// Background reports whether the run is a detached wtx _run-setup, which
// logs to a file, rather than a foreground run in someone's terminal. A nil
// state is neither.
func (s *SetupState) Background() bool {
	return s != nil && s.LogFile != ""
}

// Validate reports a record no run could have written: an unknown status,
// impossible hook counts, or a running record without a process.
func (s *SetupState) Validate() error {
	switch s.Status {
	case SetupRunning, SetupComplete, SetupFailed, SetupSkipped:
	default:
		return errors.New("invalid setup status")
	}
	if s.HooksCompleted < 0 || s.HooksTotal < 0 || s.HooksCompleted > s.HooksTotal || (s.Status == SetupRunning && s.PID <= 0) {
		return errors.New("invalid setup counters or process ID")
	}
	return nil
}

// Percent is the share of hooks completed. It is capped at 99 while setup is
// running, because parallel hooks run as one batch after the serial ones.
// ok is false when the hook count is unknown.
func (s *SetupState) Percent() (pct int, ok bool) {
	if s == nil || s.HooksTotal <= 0 {
		return 0, false
	}
	pct = 100 * s.HooksCompleted / s.HooksTotal
	if !s.Settled() {
		pct = min(pct, 99)
	}
	return pct, true
}

// SetupStatePath returns the path to the setup state file for a worktree.
func SetupStatePath(worktreePath string) string {
	return filepath.Join(worktreePath, SetupStateFile)
}

// SetupLogPath returns the path to the setup log file for a worktree.
func SetupLogPath(worktreePath string) string {
	return filepath.Join(worktreePath, SetupLogFile)
}

// SetupLog returns the setup log for a worktree, given its resolved state, or
// "" when there is none. A foreground run logs to the terminal, so any log on
// disk is stale. Without a state, the default log location is used. Only the
// base name of LogFile is used: the log lives in the worktree, and the
// recorded absolute path goes stale when the worktree moves (wtx doctor
// --migrate-home).
func SetupLog(worktreePath string, state *SetupState) string {
	path := SetupLogPath(worktreePath)
	if state != nil {
		if !state.Background() {
			return ""
		}
		path = filepath.Join(worktreePath, filepath.Base(state.LogFile))
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

// EncodeSetupState returns the on-disk form of a setup state.
func EncodeSetupState(state *SetupState) ([]byte, error) {
	data, err := json.MarshalIndent(state, "", "  ")
	return append(data, '\n'), err
}

// WriteSetupState atomically writes the setup state to the worktree directory.
func WriteSetupState(worktreePath string, state *SetupState) error {
	data, err := EncodeSetupState(state)
	if err != nil {
		return err
	}

	target := SetupStatePath(worktreePath)
	tmp := target + ".tmp"

	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// ReadSetupState reads the setup state from the worktree directory.
// Returns nil with no error if the state file does not exist.
func ReadSetupState(worktreePath string) (*SetupState, error) {
	data, err := os.ReadFile(SetupStatePath(worktreePath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var state SetupState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// ResolveSetupStatus reads the setup state and resolves stale processes.
// If the state is "running" but the PID is no longer alive, the returned
// state reflects "failed" but no write is performed. Call ReconcileSetupState
// to persist the resolution.
func ResolveSetupStatus(worktreePath string) (*SetupState, error) {
	state, err := ReadSetupState(worktreePath)
	if err != nil || state == nil {
		return state, err
	}

	ResolveDeadSetupProcess(state)
	return state, nil
}

// ObserveSetupState is the setup state to report for a worktree: nil when no
// setup is recorded, and a failed state when the record cannot be read, so
// nothing treats a worktree with a corrupt record as ready. That failed state
// has no times: the record does not say when setup started or ended.
func ObserveSetupState(worktreePath string) *SetupState {
	state, err := ResolveSetupStatus(worktreePath)
	if err != nil {
		return failedObservation("cannot read setup state: "+err.Error(), time.Time{})
	}
	return state
}

// ResolveDeadSetupProcess fails a running record whose process has exited,
// reporting whether it did.
func ResolveDeadSetupProcess(state *SetupState) bool {
	if state.Settled() || IsProcessAlive(state.PID) {
		return false
	}
	return state.Fail(staleProcessError)
}

// ReconcileSetupState reads the setup state, resolves stale processes,
// and persists the resolution to disk.
func ReconcileSetupState(worktreePath string) (*SetupState, error) {
	state, err := ReadSetupState(worktreePath)
	if err != nil || state == nil {
		return state, err
	}
	if ResolveDeadSetupProcess(state) {
		if err := WriteSetupState(worktreePath, state); err != nil {
			return state, err
		}
	}
	return state, nil
}
