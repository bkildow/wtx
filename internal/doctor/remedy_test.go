package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/ui"
)

// shellArgs returns the arguments sh splits cmd into.
func shellArgs(t *testing.T, cmd string) []string {
	t.Helper()
	out, err := exec.Command("sh", "-c", `printf '%s\0' `+cmd).Output()
	if err != nil {
		t.Fatalf("sh %s: %v", cmd, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

func TestRemedyCommandsAreShellSafe(t *testing.T) {
	path := "/tmp/a $b `c` d\\e/it's"
	tests := []struct {
		name, cmd string
		want      []string
	}{
		{"prune dry run", pruneCommand(path) + " --dry-run", []string{"git", "--git-dir=" + path, "worktree", "prune", "--dry-run"}},
		{"prune", pruneCommand(path), []string{"git", "--git-dir=" + path, "worktree", "prune"}},
		{"unlock", unlockCommand(path), []string{"git", "worktree", "unlock", path}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shellArgs(t, tt.cmd); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("sh split %s into %q, want %q", tt.cmd, got, tt.want)
			}
		})
	}
}

// TestPruneRemedyQuotesGitDir runs doctor in a project whose path needs
// quoting and checks the printed prune commands survive a paste.
func TestPruneRemedyQuotesGitDir(t *testing.T) {
	old, _, _ := fixture(t, false)
	root := filepath.Join(filepath.Dir(old), "my $proj")
	if err := os.Rename(old, root); err != nil {
		t.Fatal(err)
	}
	gitDir := filepath.Join(root, ".git")
	wt := filepath.Join(root, "worktrees", "gone")
	gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", "gone", wt)
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}
	r := Run(context.Background(), Options{StartDir: root})
	f := finding(r, "git.worktrees", ui.CanonicalPath(wt))
	if f == nil {
		t.Fatalf("missing registration finding: %+v", r.Findings)
	}
	quoted := "git --git-dir='" + ui.CanonicalPath(gitDir) + "' worktree prune"
	if want := "Review " + quoted + " --dry-run, then " + quoted + "."; f.Remedy != want {
		t.Fatalf("remedy %q, want %q", f.Remedy, want)
	}
}
