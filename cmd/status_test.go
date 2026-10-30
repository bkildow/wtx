package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
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

func TestGitErrorReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "fatal line in stderr wins",
			err:  errors.New("git -C /x status --porcelain: exit status 128\nfatal: gitfile does not point to a valid repository\n"),
			want: "fatal: gitfile does not point to a valid repository",
		},
		{
			name: "no fatal line keeps the first line",
			err:  errors.New("git -C /x log -1: exit status 1\nerror: something\n"),
			want: "git -C /x log -1: exit status 1",
		},
		{
			name: "single line",
			err:  errors.New("boom"),
			want: "boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gitErrorReason(tt.err); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// addBrokenWorktree adds an "alpha" worktree under root whose .git points
// nowhere, so git cannot read its status.
func addBrokenWorktree(t *testing.T, root string) {
	t.Helper()
	broken := filepath.Join(root, "worktrees", "alpha")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-b", "alpha", broken).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: /nonexistent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStatusBrokenWorktree(t *testing.T) {
	worktree := newSetupProject(t)
	addBrokenWorktree(t, filepath.Dir(filepath.Dir(worktree)))
	t.Setenv("WTX_NO_DISK_WARN", "1")
	out := captureUI(t)

	command := newStatusCmd()
	command.SetContext(context.Background())
	if err := runStatus(command, nil); err != nil {
		t.Fatalf("status should not fail on one broken worktree: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Could not read status for alpha: fatal:") {
		t.Errorf("missing broken-worktree hint:\n%s", got)
	}
	rows := map[string]string{"alpha": "error", "feature": "clean"}
	for branch, want := range rows {
		found := false
		for line := range strings.Lines(got) {
			if strings.Contains(line, branch) && !strings.Contains(line, "Could not read") {
				found = true
				if !strings.Contains(line, want) {
					t.Errorf("%s row should show %q: %q", branch, want, line)
				}
			}
		}
		if !found {
			t.Errorf("missing %s row:\n%s", branch, got)
		}
	}
}

func TestStatusJSONSetup(t *testing.T) {
	tests := []struct {
		name  string
		state *project.SetupState
	}{
		{name: "no state"},
		{name: "complete", state: &project.SetupState{Status: project.SetupComplete, HooksTotal: 2, HooksCompleted: 2}},
		{name: "failed", state: &project.SetupState{Status: project.SetupFailed, HooksTotal: 2, HooksCompleted: 1, Error: "hook failed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktree := newSetupProject(t)
			if tt.state != nil {
				tt.state.LogFile = project.SetupLogPath(worktree)
				writeSetupFixture(t, worktree, tt.state, "output\n")
			}

			out := runJSON(t, newStatusCmd)
			var report statusReport
			decodeJSON(t, out, &report)
			if report.SchemaVersion != 1 {
				t.Errorf("schema_version = %d, want 1", report.SchemaVersion)
			}
			if len(report.Worktrees) != 1 {
				t.Fatalf("got %d worktrees, want 1:\n%s", len(report.Worktrees), out)
			}
			entry := report.Worktrees[0]
			if entry.Branch != "feature" || !samePath(t, entry.Path, worktree) || len(entry.Head) != 40 {
				t.Errorf("entry = %+v", entry)
			}
			if entry.Dirty == nil || entry.LastCommitAge == nil || entry.Error != "" {
				t.Errorf("readable worktree should have dirty and last_commit_age and no error: %+v", entry)
			}

			if tt.state == nil {
				if entry.Setup != nil || !strings.Contains(out, `"setup": null`) {
					t.Errorf("missing state should be null:\n%s", out)
				}
				return
			}
			if entry.Setup == nil {
				t.Fatalf("missing setup:\n%s", out)
			}
			got := *entry.Setup
			if got.Status != tt.state.Status || got.Error != tt.state.Error || !samePath(t, got.LogFile, tt.state.LogFile) ||
				got.HooksTotal != tt.state.HooksTotal || got.HooksCompleted != tt.state.HooksCompleted {
				t.Errorf("setup = %+v, want %+v", got, *tt.state)
			}
		})
	}
}

// A worktree moved by wtx doctor --migrate-home keeps its old log path in
// the state file; JSON reports where the log is now.
func TestStatusJSONMovedLogFile(t *testing.T) {
	worktree := newSetupProject(t)
	state := &project.SetupState{Status: project.SetupFailed, LogFile: "/old/home/feature/" + project.SetupLogFile}
	writeSetupFixture(t, worktree, state, "output\n")

	var report statusReport
	out := runJSON(t, newStatusCmd)
	decodeJSON(t, out, &report)
	if len(report.Worktrees) != 1 || report.Worktrees[0].Setup == nil {
		t.Fatalf("want one entry with setup:\n%s", out)
	}
	if got := report.Worktrees[0].Setup.LogFile; !samePath(t, got, project.SetupLogPath(worktree)) {
		t.Errorf("log_file = %q, want the log in the worktree", got)
	}
}

func TestStatusJSONBrokenWorktree(t *testing.T) {
	worktree := newSetupProject(t)
	addBrokenWorktree(t, filepath.Dir(filepath.Dir(worktree)))

	out := runJSON(t, newStatusCmd)
	var report statusReport
	decodeJSON(t, out, &report)
	entries := map[string]statusEntry{}
	for _, entry := range report.Worktrees {
		entries[entry.Branch] = entry
	}

	alpha, ok := entries["alpha"]
	if !ok {
		t.Fatalf("missing alpha entry:\n%s", out)
	}
	if alpha.Dirty != nil || alpha.LastCommitAge != nil || !strings.HasPrefix(alpha.Error, "fatal:") {
		t.Errorf("broken entry should have null fields and a fatal error: %+v", alpha)
	}
	if feature := entries["feature"]; feature.Dirty == nil || *feature.Dirty || feature.Error != "" {
		t.Errorf("feature entry should be clean: %+v", feature)
	}
}

func TestStatusJSONEmpty(t *testing.T) {
	worktree := newSetupProject(t)
	if out, err := exec.Command("git", "worktree", "remove", "--force", worktree).CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %v\n%s", err, out)
	}

	out := runJSON(t, newStatusCmd)
	if !strings.Contains(out, `"worktrees": []`) {
		t.Errorf("empty project should give an empty array:\n%s", out)
	}
	var report statusReport
	decodeJSON(t, out, &report)
}
