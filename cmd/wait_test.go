package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/termstatus"
)

func TestWait(t *testing.T) {
	started := time.Now().Add(-time.Minute)
	tests := []struct {
		name     string
		state    *project.SetupState
		args     []string
		timeout  string
		wantCode int
		wantUI   string
		wantErr  string
	}{
		{
			name:   "complete",
			state:  &project.SetupState{Status: project.SetupComplete, StartedAt: started, CompletedAt: started.Add(41 * time.Second)},
			args:   []string{"feature"},
			wantUI: "feature: setup completed in 41 seconds",
		},
		{
			name:   "no setup recorded",
			args:   []string{"feature"},
			wantUI: "feature: no setup recorded",
		},
		{
			name:   "skipped",
			state:  &project.SetupState{Status: project.SetupSkipped},
			args:   []string{"feature"},
			wantUI: "feature: setup skipped",
		},
		{
			name:     "failed",
			state:    &project.SetupState{Status: project.SetupFailed, Error: "1 setup hook(s) failed"},
			args:     []string{"feature"},
			wantCode: exitSetupFailed,
			wantUI:   "Setup failed for feature: 1 setup hook(s) failed",
		},
		{
			// PID is this test process, so the running state stays running.
			name:     "still running at the timeout",
			state:    &project.SetupState{Status: project.SetupRunning, PID: os.Getpid(), HooksTotal: 4, HooksCompleted: 1},
			args:     []string{"feature"},
			timeout:  "0",
			wantCode: exitStillRunning,
			wantUI:   "feature: hook 2/4 (still running)",
		},
		{
			name:     "dead setup process",
			state:    &project.SetupState{Status: project.SetupRunning, PID: 1 << 30},
			args:     []string{"feature"},
			wantCode: exitSetupFailed,
			wantUI:   "setup process exited unexpectedly",
		},
		{
			name:    "unknown worktree",
			args:    []string{"nope"},
			wantErr: "worktree not found: nope",
		},
		{
			name:    "outside a worktree without a terminal",
			wantErr: "not inside a managed worktree: name a worktree or pass --all",
		},
		{
			name:    "names and --all",
			args:    []string{"feature", "--all"},
			wantErr: "pass worktree names or --all, not both",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktree := newSetupProject(t)
			writeSetupFixture(t, worktree, tt.state, "")
			info := captureUI(t)

			args := tt.args
			if tt.timeout != "" {
				args = append(args, "--timeout", tt.timeout)
			}
			command := newWaitCmd()
			command.SetContext(context.Background())
			command.SetOut(&bytes.Buffer{})
			if err := command.ParseFlags(args); err != nil {
				t.Fatal(err)
			}
			err := runWait(command, command.Flags().Args())

			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if got := exitCodeOf(err); got != tt.wantCode {
				t.Errorf("exit code = %d (err %v), want %d", got, err, tt.wantCode)
			}
			if !strings.Contains(info.String(), tt.wantUI) {
				t.Errorf("ui output = %q, want it to contain %q", info.String(), tt.wantUI)
			}
		})
	}
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	return ExitCode(err)
}

func TestWaitJSON(t *testing.T) {
	worktree := newSetupProject(t)
	t.Chdir(worktree)
	writeSetupFixture(t, worktree, &project.SetupState{Status: project.SetupFailed, Error: "boom", LogFile: project.SetupLogPath(worktree)}, "log\n")
	info := captureUI(t)

	var out bytes.Buffer
	command := newWaitCmd()
	command.SetContext(context.Background())
	command.SetOut(&out)
	_ = command.Flags().Set("json", "true")
	err := runWait(command, []string{"."})
	if exitCodeOf(err) != exitSetupFailed {
		t.Fatalf("err = %v, want exit %d", err, exitSetupFailed)
	}
	if info.Len() != 0 {
		t.Errorf("--json wrote to stderr: %q", info.String())
	}

	var report waitReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if report.SchemaVersion != 1 || len(report.Worktrees) != 1 {
		t.Fatalf("report = %+v", report)
	}
	got := report.Worktrees[0]
	if got.Branch != "feature" || got.Setup == nil || got.Setup.Status != project.SetupFailed || got.Setup.LogFile == "" {
		t.Errorf("entry = %+v, setup = %+v", got, got.Setup)
	}
}

func TestWaitExitCode(t *testing.T) {
	running := project.SetupSnapshot{State: &project.SetupState{Status: project.SetupRunning}}
	failed := project.SetupSnapshot{State: &project.SetupState{Status: project.SetupFailed}}
	ready := project.SetupSnapshot{}
	pending := project.SetupSnapshot{Pending: true}
	tests := []struct {
		name     string
		snaps    []project.SetupSnapshot
		timedOut bool
		want     int
	}{
		{"all ready", []project.SetupSnapshot{ready, ready}, false, 0},
		{"failure beats running", []project.SetupSnapshot{running, failed}, true, exitSetupFailed},
		{"timed out", []project.SetupSnapshot{ready, running}, true, exitStillRunning},
		{"waiting for setup to start", []project.SetupSnapshot{pending}, true, exitStillRunning},
		{"interrupted", []project.SetupSnapshot{running}, false, exitInterrupted},
	}
	for _, tt := range tests {
		if got := waitExitCode(tt.snaps, tt.timedOut); got != tt.want {
			t.Errorf("%s: waitExitCode = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestSetupReports(t *testing.T) {
	snap := func(branch string, s *project.SetupState) project.SetupSnapshot {
		return project.SetupSnapshot{SetupTarget: project.SetupTarget{Branch: branch}, State: s}
	}
	running := snap("a", &project.SetupState{Status: project.SetupRunning, HooksTotal: 4, HooksCompleted: 1})
	complete := snap("b", &project.SetupState{Status: project.SetupComplete, HooksTotal: 4, HooksCompleted: 4})
	failed := snap("c", &project.SetupState{Status: project.SetupFailed, Error: "1 setup hook(s) failed\nmore"})

	if r := setupReport("", running); r.State != termstatus.Working || r.Msg != "a: hook 2/4" || r.Progress != termstatus.Percent(25) || r.Title != "" {
		t.Errorf("running report = %+v", r)
	}
	if r := setupReport("c-1", failed); r.State != termstatus.Error || r.Msg != "c: setup failed: 1 setup hook(s) failed" || r.Title != "c" {
		t.Errorf("failed report = %+v", r)
	}
	if r := setupReport("", project.SetupSnapshot{SetupTarget: project.SetupTarget{Branch: "d"}, Pending: true}); r.State != termstatus.Working || r.Progress != (termstatus.Progress{}) {
		t.Errorf("pending report = %+v", r)
	}

	if r := rollupReport([]project.SetupSnapshot{running, complete}); r.State != termstatus.Working || r.Msg != "1 of 2 worktrees ready" || r.Progress != termstatus.Percent(62) {
		t.Errorf("rollup while running = %+v", r)
	}
	pending := project.SetupSnapshot{SetupTarget: project.SetupTarget{Branch: "d"}, Pending: true}
	if r := rollupReport([]project.SetupSnapshot{complete, pending}); r.State != termstatus.Working || r.Progress != (termstatus.Progress{}) {
		t.Errorf("rollup with a pending worktree = %+v, want indeterminate progress", r)
	}
	if r := setupReport("", snap("e", &project.SetupState{Status: project.SetupRunning})); r.Msg != "e: setup running" {
		t.Errorf("running report without hooks = %+v", r)
	}
	if r := rollupReport([]project.SetupSnapshot{running, failed}); r.State != termstatus.Error || r.Msg != "c: setup failed" {
		t.Errorf("rollup with a failure = %+v", r)
	}
	if r := rollupReport([]project.SetupSnapshot{complete, complete}); r.State != termstatus.Done || r.Msg != "2 worktrees ready" {
		t.Errorf("rollup when ready = %+v", r)
	}
}

func TestExitCode(t *testing.T) {
	if got := ExitCode(exitError(75)); got != 75 {
		t.Errorf("ExitCode(exitError(75)) = %d", got)
	}
	if got := ExitCode(os.ErrNotExist); got != 1 {
		t.Errorf("ExitCode(other) = %d, want 1", got)
	}
}

// TestForegroundSetupRecordsProgress checks that a foreground setup writes a
// running state while its hooks run, so wtx wait in another terminal sees it.
func TestForegroundSetupRecordsProgress(t *testing.T) {
	worktree := newSetupProject(t)
	config := "git_dir: .git\nworktree_dir: worktrees\nshared_dir: shared\nsetup:\n  - cp .wtx-setup.json during.json\n"
	if err := os.WriteFile(".worktree.yml", []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	captureUI(t)

	command := newSetupCmd()
	command.SetContext(context.Background())
	_ = command.Flags().Set("foreground", "true")
	if err := runSetup(command, []string{"feature"}); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(worktree + "/during.json")
	if err != nil {
		t.Fatal(err)
	}
	var during project.SetupState
	if err := json.Unmarshal(data, &during); err != nil {
		t.Fatal(err)
	}
	if during.Status != project.SetupRunning || during.PID != os.Getpid() || during.LogFile != "" || during.HooksTotal != 1 {
		t.Errorf("state during the hook = %+v, want running with this PID and no log file", during)
	}
	final, err := project.ReadSetupState(worktree)
	if err != nil || final == nil || final.Status != project.SetupComplete || final.HooksCompleted != 1 {
		t.Errorf("final state = %+v, %v", final, err)
	}
}
