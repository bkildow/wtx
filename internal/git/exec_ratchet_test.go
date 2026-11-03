package git

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gitExec matches a git process started by hand instead of through
// Runner.command or gitAt, which apply --git-dir or discoveryEnv, BatchMode,
// Quiet logging and the shared error format.
var gitExec = regexp.MustCompile(`exec\.Command(Context)?\([^)]*"git"`)

// gitExecBaseline is how many hand-built git commands internal/git still
// has. Lower it when you route one through the builders; never raise it.
const gitExecBaseline = 13

// TestGitCommandsUseTheBuilders keeps new git calls on Runner.command and
// gitAt: none outside internal/git, and no more inside it than today.
func TestGitCommandsUseTheBuilders(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	inside := 0
	err = filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != moduleRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(moduleRoot, path)
		for i, line := range strings.Split(string(data), "\n") {
			if !gitExec.MatchString(line) {
				continue
			}
			if filepath.Dir(rel) == filepath.Join("internal", "git") {
				inside++
				continue
			}
			t.Errorf("%s:%d runs git directly; add a git.Runner method (built on Runner.command, or gitAt for git -C) in internal/git", rel, i+1)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case inside > gitExecBaseline:
		t.Errorf("internal/git builds %d git commands by hand, more than the %d allowed; build the new one with Runner.command (or gitAt for git -C) so it honors BatchMode, Quiet and dry-run", inside, gitExecBaseline)
	case inside < gitExecBaseline:
		t.Logf("internal/git builds %d git commands by hand; lower gitExecBaseline to %d", inside, inside)
	}
}
