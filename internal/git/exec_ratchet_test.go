package git

import (
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/testenv"
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
	inside := 0
	testenv.GoFiles(t, func(rel string, data []byte) {
		if strings.HasSuffix(rel, "_test.go") {
			return
		}
		for i, line := range strings.Split(string(data), "\n") {
			if !gitExec.MatchString(line) {
				continue
			}
			if path.Dir(rel) == "internal/git" {
				inside++
				continue
			}
			t.Errorf("%s:%d runs git directly; add a git.Runner method (built on Runner.command, or gitAt for git -C) in internal/git", rel, i+1)
		}
	})
	switch {
	case inside > gitExecBaseline:
		t.Errorf("internal/git builds %d git commands by hand, more than the %d allowed; build the new one with Runner.command (or gitAt for git -C) so it honors BatchMode, Quiet and dry-run", inside, gitExecBaseline)
	case inside < gitExecBaseline:
		t.Logf("internal/git builds %d git commands by hand; lower gitExecBaseline to %d", inside, inside)
	}
}
