package cmd

import (
	"encoding/json"
	"io"
	"time"

	"github.com/bkildow/wtx/internal/project"
)

// writeJSON writes v to w as one JSON document indented two spaces.
func writeJSON(w io.Writer, v any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}

// setupJSON is a worktree's setup state as status --json and wait --json
// report it. It is separate from project.SetupState, the on-disk record, so
// a change to that file does not change the public output.
type setupJSON struct {
	Status         project.SetupStatus `json:"status"`
	StartedAt      time.Time           `json:"started_at,omitzero"`
	CompletedAt    time.Time           `json:"completed_at,omitzero"`
	HooksTotal     int                 `json:"hooks_total"`
	HooksCompleted int                 `json:"hooks_completed"`
	Error          string              `json:"error,omitempty"`
	LogFile        string              `json:"log_file"`
}

// newSetupJSON reports state, or nil when no setup is recorded. LogFile is
// where the log is now, or "" when there is none: the recorded path goes
// stale when the worktree moves.
func newSetupJSON(worktreePath string, state *project.SetupState) *setupJSON {
	if state == nil {
		return nil
	}
	return &setupJSON{
		Status:         state.Status,
		StartedAt:      state.StartedAt,
		CompletedAt:    state.CompletedAt,
		HooksTotal:     state.HooksTotal,
		HooksCompleted: state.HooksCompleted,
		Error:          state.Error,
		LogFile:        project.SetupLog(worktreePath, state),
	}
}
