package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndReadSetupState(t *testing.T) {
	dir := t.TempDir()
	state := &SetupState{
		Status:         SetupRunning,
		PID:            12345,
		StartedAt:      time.Now().Truncate(time.Millisecond),
		HooksTotal:     3,
		HooksCompleted: 1,
		LogFile:        SetupLogPath(dir),
	}

	if err := WriteSetupState(dir, state); err != nil {
		t.Fatalf("WriteSetupState: %v", err)
	}

	got, err := ReadSetupState(dir)
	if err != nil {
		t.Fatalf("ReadSetupState: %v", err)
	}

	if got.Status != state.Status {
		t.Errorf("Status = %q, want %q", got.Status, state.Status)
	}
	if got.PID != state.PID {
		t.Errorf("PID = %d, want %d", got.PID, state.PID)
	}
	if got.HooksTotal != state.HooksTotal {
		t.Errorf("HooksTotal = %d, want %d", got.HooksTotal, state.HooksTotal)
	}
	if got.HooksCompleted != state.HooksCompleted {
		t.Errorf("HooksCompleted = %d, want %d", got.HooksCompleted, state.HooksCompleted)
	}
}

func TestReadSetupStateMissing(t *testing.T) {
	dir := t.TempDir()

	state, err := ReadSetupState(dir)
	if err != nil {
		t.Fatalf("ReadSetupState: %v", err)
	}
	if state != nil {
		t.Fatalf("expected nil state for missing file, got %+v", state)
	}
}

func TestReadSetupStateInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SetupStateFile)
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ReadSetupState(dir)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestWriteSetupStateAtomic(t *testing.T) {
	dir := t.TempDir()
	state := &SetupState{
		Status:    SetupComplete,
		PID:       1,
		StartedAt: time.Now(),
	}

	if err := WriteSetupState(dir, state); err != nil {
		t.Fatal(err)
	}

	// Verify the temp file was cleaned up
	tmp := SetupStatePath(dir) + ".tmp"
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("temp file should not exist after atomic write")
	}

	// Verify the final file exists
	if _, err := os.Stat(SetupStatePath(dir)); err != nil {
		t.Errorf("state file should exist: %v", err)
	}
}

func TestStaleProcessDetection(t *testing.T) {
	dir := t.TempDir()
	// Use a PID that is almost certainly not running (max int-ish)
	state := &SetupState{
		Status:    SetupRunning,
		PID:       99999999,
		StartedAt: time.Now(),
	}

	if err := WriteSetupState(dir, state); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveSetupStatus(dir)
	if err != nil {
		t.Fatalf("ResolveSetupStatus: %v", err)
	}

	if got.Status != SetupFailed {
		t.Errorf("Status = %q, want %q (stale PID should resolve to failed)", got.Status, SetupFailed)
	}
	if got.Error == "" {
		t.Error("expected error message for stale process")
	}

	// ResolveSetupStatus is read-only — disk should still show running
	persisted, err := ReadSetupState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != SetupRunning {
		t.Error("ResolveSetupStatus should not write to disk")
	}
}

func TestReconcileSetupState(t *testing.T) {
	dir := t.TempDir()
	state := &SetupState{
		Status:    SetupRunning,
		PID:       99999999,
		StartedAt: time.Now(),
	}

	if err := WriteSetupState(dir, state); err != nil {
		t.Fatal(err)
	}

	got, err := ReconcileSetupState(dir)
	if err != nil {
		t.Fatalf("ReconcileSetupState: %v", err)
	}

	if got.Status != SetupFailed {
		t.Errorf("Status = %q, want %q", got.Status, SetupFailed)
	}

	// Verify it was persisted to disk
	persisted, err := ReadSetupState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != SetupFailed {
		t.Error("ReconcileSetupState should persist stale detection to disk")
	}
}

func TestIsProcessAlive(t *testing.T) {
	// Current process should be alive
	if !IsProcessAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}

	// Zero/negative PID should not be alive
	if IsProcessAlive(0) {
		t.Error("PID 0 should not be considered alive")
	}
	if IsProcessAlive(-1) {
		t.Error("PID -1 should not be considered alive")
	}
}

func TestResolveSetupStatusMissing(t *testing.T) {
	dir := t.TempDir()

	state, err := ResolveSetupStatus(dir)
	if err != nil {
		t.Fatalf("ResolveSetupStatus: %v", err)
	}
	if state != nil {
		t.Fatalf("expected nil for missing state file, got %+v", state)
	}
}

func TestResolveSetupStatusComplete(t *testing.T) {
	dir := t.TempDir()
	state := &SetupState{
		Status:      SetupComplete,
		PID:         99999999,
		StartedAt:   time.Now(),
		CompletedAt: time.Now(),
	}

	if err := WriteSetupState(dir, state); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveSetupStatus(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Should remain complete even with a dead PID (only running gets checked)
	if got.Status != SetupComplete {
		t.Errorf("Status = %q, want %q", got.Status, SetupComplete)
	}
}

func TestSetupStatePath(t *testing.T) {
	dir := t.TempDir()
	got := SetupStatePath(dir)
	want := filepath.Join(dir, SetupStateFile)
	if got != want {
		t.Errorf("SetupStatePath = %q, want %q", got, want)
	}
}

func TestSetupLogPath(t *testing.T) {
	dir := t.TempDir()
	got := SetupLogPath(dir)
	want := filepath.Join(dir, SetupLogFile)
	if got != want {
		t.Errorf("SetupLogPath = %q, want %q", got, want)
	}
}

func TestSetupLog(t *testing.T) {
	tests := []struct {
		name     string
		noState  bool   // no state file, as when setup never ran
		logFile  string // state's LogFile, relative to the worktree ("" for foreground)
		writeLog string // log file present on disk, relative to the worktree
		want     string // expected log, relative to the worktree ("" for none)
	}{
		{name: "state log file exists", logFile: SetupLogFile, writeLog: SetupLogFile, want: SetupLogFile},
		{name: "state log file missing", logFile: SetupLogFile},
		{name: "worktree moved since setup", logFile: filepath.Join("..", "old-location", SetupLogFile), writeLog: SetupLogFile, want: SetupLogFile},
		{name: "foreground state ignores stale log", writeLog: SetupLogFile},
		{name: "no state falls back to default log", noState: true, writeLog: SetupLogFile, want: SetupLogFile},
		{name: "no state and no log", noState: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			abs := func(name string) string {
				if name == "" {
					return ""
				}
				return filepath.Join(dir, name)
			}
			if tt.writeLog != "" {
				if err := os.WriteFile(abs(tt.writeLog), []byte("log"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var state *SetupState
			if !tt.noState {
				state = &SetupState{Status: SetupFailed, LogFile: abs(tt.logFile)}
			}
			if got := SetupLog(dir, state); got != abs(tt.want) {
				t.Errorf("SetupLog() = %q, want %q", got, abs(tt.want))
			}
		})
	}
}

// TestSetupLifecycle checks every edge of the setup lifecycle: running moves
// to running, complete or failed, and settled records never change.
func TestSetupLifecycle(t *testing.T) {
	ops := map[string]func(*SetupState){
		"progress":   func(s *SetupState) { s.Progress(1) },
		"finish ok":  func(s *SetupState) { s.Finish(nil) },
		"finish err": func(s *SetupState) { s.Finish(errors.New("hook failed")) },
		"fail":       func(s *SetupState) { s.Fail("stopped") },
	}
	want := map[SetupStatus]map[string]SetupStatus{
		SetupRunning:  {"progress": SetupRunning, "finish ok": SetupComplete, "finish err": SetupFailed, "fail": SetupFailed},
		SetupComplete: {},
		SetupFailed:   {},
		SetupSkipped:  {},
	}
	for from, edges := range want {
		for name, op := range ops {
			t.Run(string(from)+"/"+name, func(t *testing.T) {
				s := &SetupState{Status: from, PID: 1, HooksTotal: 2, Error: "before"}
				before := *s
				to, legal := edges[name]
				op(s)
				if !legal {
					if *s != before {
						t.Fatalf("illegal %s changed the record: %+v", name, *s)
					}
					return
				}
				if s.Status != to {
					t.Fatalf("status = %s, want %s", s.Status, to)
				}
			})
		}
	}
}

func TestSetupFinishCompletesEveryHook(t *testing.T) {
	for _, err := range []error{nil, errors.New("1 setup hook(s) failed")} {
		s := StartForegroundSetup(1, 3)
		s.Progress(1)
		s.Finish(err)
		if s.HooksCompleted != 3 || s.CompletedAt.IsZero() {
			t.Errorf("Finish(%v) = %+v, want every hook completed and a completion time", err, s)
		}
		if err != nil && s.Error != err.Error() {
			t.Errorf("Error = %q, want %q", s.Error, err)
		}
		if err := s.Validate(); err != nil {
			t.Errorf("Validate after Finish: %v", err)
		}
	}
}

func TestSetupStartsAndSettles(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name                string
		state               *SetupState
		settled, background bool
	}{
		{"no record", nil, true, false},
		{"foreground", StartForegroundSetup(1, 2), false, false},
		{"background", StartBackgroundSetup(dir, 1, 2), false, true},
		{"skipped", SkipSetup(), true, false},
	}
	for _, tt := range tests {
		if got := tt.state.Settled(); got != tt.settled {
			t.Errorf("%s: Settled = %v, want %v", tt.name, got, tt.settled)
		}
		if tt.state != nil && tt.state.Background() != tt.background {
			t.Errorf("%s: Background = %v, want %v", tt.name, tt.state.Background(), tt.background)
		}
	}
}

func TestSetupValidate(t *testing.T) {
	tests := []struct {
		name  string
		state SetupState
		ok    bool
	}{
		{"running", SetupState{Status: SetupRunning, PID: 1, HooksTotal: 2, HooksCompleted: 1}, true},
		{"unknown status", SetupState{Status: "paused"}, false},
		{"running without process", SetupState{Status: SetupRunning}, false},
		{"more completed than total", SetupState{Status: SetupComplete, HooksTotal: 1, HooksCompleted: 2}, false},
		{"negative total", SetupState{Status: SetupFailed, HooksTotal: -1}, false},
	}
	for _, tt := range tests {
		if err := tt.state.Validate(); (err == nil) != tt.ok {
			t.Errorf("%s: Validate = %v, want ok %v", tt.name, err, tt.ok)
		}
	}
}

func TestSetupFailReportsWhetherRunning(t *testing.T) {
	if !StartForegroundSetup(1, 1).Fail("stopped") {
		t.Error("Fail on a running setup = false, want true")
	}
	if SkipSetup().Fail("stopped") {
		t.Error("Fail on a skipped setup = true, want false")
	}
}
