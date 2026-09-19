package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/project"
)

func TestSetupLegacyMigration(t *testing.T) {
	for _, mode := range []string{"dry", "run", "live"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
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
			if err := os.WriteFile(filepath.Join(root, ".worktree.yml"), []byte("git_dir: .git\nsetup:\n  - 'true'\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			exclude := filepath.Join(root, ".git", "info", "exclude")
			oldExclude := "# wt-cli managed files\n.wt-setup.json\n.wt-setup.log\n"
			if err := os.WriteFile(exclude, []byte(oldExclude), 0o644); err != nil {
				t.Fatal(err)
			}
			pid := -1
			if mode == "live" {
				pid = os.Getpid()
			}
			legacy := filepath.Join(worktree, ".wt-setup.json")
			oldState := `{"status":"running","pid":` + strconv.Itoa(pid) + `}`
			if err := os.WriteFile(legacy, []byte(oldState), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Chdir(root)
			previousDry := dryRun
			dryRun = mode == "dry"
			t.Cleanup(func() { dryRun = previousDry })
			command := newSetupCmd()
			command.SetContext(context.Background())
			err := runSetup(command, []string{"feature"})
			if mode == "live" {
				if err == nil || !strings.Contains(err.Error(), "setup already running") {
					t.Fatalf("expected running guard, got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(legacy)
			if err != nil || string(data) != oldState {
				t.Fatalf("legacy file changed: %s, %v", data, err)
			}
			if mode != "run" {
				if _, err := os.Stat(project.SetupStatePath(worktree)); !os.IsNotExist(err) {
					t.Fatalf("unexpected new state: %v", err)
				}
				data, err := os.ReadFile(exclude)
				if err != nil || string(data) != oldExclude {
					t.Fatalf("exclusions changed: %s, %v", data, err)
				}
			} else {
				state, err := project.ReadSetupState(worktree)
				if err != nil || state == nil || state.Status != project.SetupComplete {
					t.Fatalf("state = %+v, %v", state, err)
				}
				runGit("-C", worktree, "check-ignore", project.SetupStateFile, project.SetupLogFile)
			}
		})
	}
}
