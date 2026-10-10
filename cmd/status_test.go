package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/project"
)

func TestSetupFailureHint(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, project.SetupLogFile)
	if err := os.WriteFile(logPath, []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		state *project.SetupState
		want  string
	}{
		{
			name:  "background failure points at the log",
			state: &project.SetupState{Status: project.SetupFailed, Error: "hook 'npm ci' failed: exit status 1", LogFile: logPath},
			want:  "Setup failed for feat: hook 'npm ci' failed: exit status 1 — run 'wtx logs feat'",
		},
		{
			name:  "multi-line error keeps the first line",
			state: &project.SetupState{Status: project.SetupFailed, Error: "first\nsecond", LogFile: logPath},
			want:  "Setup failed for feat: first — run 'wtx logs feat'",
		},
		{
			name:  "empty error",
			state: &project.SetupState{Status: project.SetupFailed, LogFile: logPath},
			want:  "Setup failed for feat — run 'wtx logs feat'",
		},
		{
			name:  "foreground failure suggests a re-run",
			state: &project.SetupState{Status: project.SetupFailed, Error: "boom"},
			want:  "Setup failed for feat: boom — re-run with 'wtx setup feat --foreground'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := setupFailureHint("feat", dir, tt.state); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestStatusSetupFailureHint(t *testing.T) {
	// runStatusWith writes the given setup state and a log, runs status, and
	// returns its output.
	runStatusWith := func(t *testing.T, state *project.SetupState) string {
		t.Helper()
		worktree := newSetupProject(t)
		t.Setenv("WTX_NO_DISK_WARN", "1")
		state.LogFile = project.SetupLogPath(worktree)
		writeSetupFixture(t, worktree, state, "output\n")
		out := captureUI(t)

		command := newStatusCmd()
		command.SetContext(context.Background())
		if err := runStatus(command, nil); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	t.Run("failed", func(t *testing.T) {
		out := runStatusWith(t, &project.SetupState{Status: project.SetupFailed, Error: "hook failed"})
		if !strings.Contains(out, "wtx logs feature") {
			t.Errorf("missing failure hint:\n%s", out)
		}
	})

	t.Run("complete", func(t *testing.T) {
		out := runStatusWith(t, &project.SetupState{Status: project.SetupComplete})
		if strings.Contains(out, "Setup failed") {
			t.Errorf("unexpected failure hint:\n%s", out)
		}
	})
}
