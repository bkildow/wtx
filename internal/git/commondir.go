package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bkildow/wtx/internal/ui"
)

// ErrNotGitRepo is returned by CommonDir when dir is not inside a git
// repository (or git is unavailable), so callers can fall back to other
// discovery strategies.
var ErrNotGitRepo = errors.New("not inside a git repository")

// CommonDir returns the absolute path of the git common directory for the
// repository containing dir: the bare repo (e.g. <root>/.bare) or the main
// .git directory, even when dir is inside a linked worktree.
//
// Unlike Runner methods it does not pass --git-dir: it is used to discover
// the repository before the git dir is known. Any failure to resolve is
// reported as ErrNotGitRepo.
func CommonDir(ctx context.Context, dir string) (string, error) {
	out, err := revParse(ctx, dir, "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", ErrNotGitRepo
	}
	// --path-format needs git >= 2.31. Older versions echo the unknown flag
	// back as output instead of failing, so accept only a single absolute
	// path and otherwise retry without it.
	if strings.Contains(out, "\n") || !filepath.IsAbs(out) {
		out, err = revParse(ctx, dir, "--git-common-dir")
		if err != nil || out == "" || strings.Contains(out, "\n") {
			return "", ErrNotGitRepo
		}
		if !filepath.IsAbs(out) {
			out = filepath.Join(dir, out)
		}
	}
	return filepath.Clean(out), nil
}

func revParse(ctx context.Context, dir string, args ...string) (string, error) {
	// Not logged: FindRoot runs this for every command.
	out, err := gitAt(ctx, dir, nil, false, append([]string{"rev-parse"}, args...)...)
	return strings.TrimSpace(out), err
}

// gitAt runs git -C dir with discoveryEnv, so dir alone selects the
// repository, and returns its raw stdout. When logged is true the command is
// printed like Runner commands.
func gitAt(ctx context.Context, dir string, stdin io.Reader, logged bool, args ...string) (string, error) {
	fullArgs := append([]string{"-C", dir}, args...)
	cmdStr := "git " + strings.Join(fullArgs, " ")
	if logged {
		ui.Command(cmdStr)
	}
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	cmd.Env = discoveryEnv()
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w\n%s", cmdStr, err, stderr.String())
	}
	return stdout.String(), nil
}

// discoveryEnv strips variables that would override repository discovery
// (e.g. GIT_DIR set when wtx runs from inside a git hook), so -C <dir>
// alone determines which repository is found.
func discoveryEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE":
			continue
		}
		env = append(env, kv)
	}
	return env
}
