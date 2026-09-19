package project

import (
	"os"
	"path/filepath"
	"strconv"
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

func TestLegacySetupStateContinuesUpdating(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, legacySetupStateFile)
	for _, status := range []SetupStatus{SetupRunning, SetupComplete} {
		data := []byte(`{"status":"` + string(status) + `","pid":` + strconv.Itoa(os.Getpid()) + `,"log_file":".wt-setup.log"}`)
		if err := os.WriteFile(legacy, data, 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := ReconcileSetupState(dir)
		if err != nil || got == nil || got.Status != status || got.LogFile != ".wt-setup.log" {
			t.Fatalf("state = %+v, err = %v", got, err)
		}
		if _, err := os.Stat(SetupStatePath(dir)); !os.IsNotExist(err) {
			t.Fatalf("legacy read created new state: %v", err)
		}
	}
	if err := WriteSetupState(dir, &SetupState{Status: SetupSkipped}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadSetupState(dir)
	if err != nil || got.Status != SetupSkipped {
		t.Fatalf("new state did not win: %+v, %v", got, err)
	}
	if err := os.WriteFile(SetupStatePath(dir), []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSetupState(dir); err == nil {
		t.Fatal("corrupt new state must not fall back")
	}
}

func TestReconcileStaleLegacyState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, legacySetupStateFile), []byte(`{"status":"running","pid":-1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReconcileSetupState(dir)
	if err != nil || got == nil || got.Status != SetupFailed {
		t.Fatalf("state = %+v, %v", got, err)
	}
	if _, err := os.Stat(SetupStatePath(dir)); err != nil {
		t.Fatal(err)
	}
}
