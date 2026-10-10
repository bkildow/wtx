package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
)

// newSetupProject creates a project with one Managed worktree on branch
// "feature", changes into the project root, and returns the worktree path.
func newSetupProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("WTX_HOME", t.TempDir())
	runGit := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", root)
	runGit("-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "initial")
	worktree := filepath.Join(root, "worktrees", "feature")
	runGit("-C", root, "worktree", "add", "-b", "feature", worktree)
	if err := os.WriteFile(filepath.Join(root, ".worktree.yml"), []byte("git_dir: .git\nworktree_dir: worktrees\nshared_dir: shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return worktree
}

// captureUI sends ui output to a buffer for the rest of the test.
func captureUI(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := ui.Output
	ui.Output = &buf
	t.Cleanup(func() { ui.Output = orig })
	return &buf
}

func writeSetupFixture(t *testing.T, worktree string, state *project.SetupState, log string) {
	t.Helper()
	if state != nil {
		if err := project.WriteSetupState(worktree, state); err != nil {
			t.Fatal(err)
		}
	}
	if log != "" {
		if err := os.WriteFile(project.SetupLogPath(worktree), []byte(log), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLogs(t *testing.T) {
	tests := []struct {
		name     string
		state    *project.SetupState // LogFile is filled in when background is set
		bg       bool
		log      string
		args     []string
		wantOut  string
		wantInfo string
		wantErr  string
	}{
		{
			name:    "prints the log named by the state",
			state:   &project.SetupState{Status: project.SetupFailed, Error: "hook failed"},
			bg:      true,
			log:     "npm ERR! boom\n",
			args:    []string{"feature"},
			wantOut: "npm ERR! boom\n",
		},
		{
			name:    "falls back to the default log without state",
			log:     "fallback output\n",
			args:    []string{"feature"},
			wantOut: "fallback output\n",
		},
		{
			name:     "foreground state has no log",
			state:    &project.SetupState{Status: project.SetupFailed, Error: "hook failed"},
			log:      "stale background output\n",
			args:     []string{"feature"},
			wantInfo: "No setup log for feature",
		},
		{
			// PID is this test process, so the running state stays running.
			name:     "background setup has not written its log yet",
			state:    &project.SetupState{Status: project.SetupRunning, PID: os.Getpid()},
			bg:       true,
			args:     []string{"feature"},
			wantInfo: "has not written any output yet",
		},
		{
			name:     "background log missing",
			state:    &project.SetupState{Status: project.SetupFailed, Error: "setup process exited unexpectedly"},
			bg:       true,
			args:     []string{"feature"},
			wantInfo: "the background setup log is missing",
		},
		{
			name:     "no setup ever ran",
			args:     []string{"feature"},
			wantInfo: "No setup log for feature",
		},
		{
			name:    "unknown worktree",
			args:    []string{"nope"},
			wantErr: "worktree not found: nope",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktree := newSetupProject(t)
			if tt.bg {
				tt.state.LogFile = project.SetupLogPath(worktree)
			}
			writeSetupFixture(t, worktree, tt.state, tt.log)
			info := captureUI(t)

			var out bytes.Buffer
			command := newLogsCmd()
			command.SetContext(context.Background())
			command.SetOut(&out)
			err := runLogs(command, tt.args)

			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.String() != tt.wantOut {
				t.Errorf("stdout = %q, want %q", out.String(), tt.wantOut)
			}
			if tt.wantInfo != "" && !strings.Contains(info.String(), tt.wantInfo) {
				t.Errorf("ui output = %q, want it to contain %q", info.String(), tt.wantInfo)
			}
		})
	}
}
