package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const (
	SetupStateFile = ".wtx-setup.json"
	SetupLogFile   = ".wtx-setup.log"

	legacySetupStateFile = ".wt-setup.json"
	legacySetupLogFile   = ".wt-setup.log"

	// staleProcessError is the sentinel error string used when a running
	// process is detected as no longer alive.
	staleProcessError = "setup process exited unexpectedly"
)

// SetupStatus represents the current state of setup hook execution.
type SetupStatus string

const (
	SetupRunning  SetupStatus = "running"
	SetupComplete SetupStatus = "complete"
	SetupFailed   SetupStatus = "failed"
	SetupSkipped  SetupStatus = "skipped"
)

// SetupState tracks the progress of setup hooks for a worktree.
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

// SetupStatePath returns the path to the setup state file for a worktree.
func SetupStatePath(worktreePath string) string {
	return filepath.Join(worktreePath, SetupStateFile)
}

// SetupLogPath returns the path to the setup log file for a worktree.
func SetupLogPath(worktreePath string) string {
	return filepath.Join(worktreePath, SetupLogFile)
}

// WriteSetupState atomically writes the setup state to the worktree directory.
func WriteSetupState(worktreePath string, state *SetupState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

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
	// Read legacy state in place so an old background process remains visible
	// as it updates its file. New runs always take precedence.
	if errors.Is(err, os.ErrNotExist) {
		data, err = os.ReadFile(filepath.Join(worktreePath, legacySetupStateFile))
	}
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

	if state.Status == SetupRunning && !IsProcessAlive(state.PID) {
		state.Status = SetupFailed
		state.Error = staleProcessError
		state.CompletedAt = time.Now()
	}

	return state, nil
}

// ReconcileSetupState reads the setup state, resolves stale processes,
// and persists the resolution to disk.
func ReconcileSetupState(worktreePath string) (*SetupState, error) {
	state, err := ResolveSetupStatus(worktreePath)
	if err != nil || state == nil {
		return state, err
	}

	// Only write if we resolved a stale running state.
	if state.Status == SetupFailed && state.Error == staleProcessError {
		if writeErr := WriteSetupState(worktreePath, state); writeErr != nil {
			return state, writeErr
		}
	}

	return state, nil
}
