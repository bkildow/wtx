package git

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// --path-format needs git >= 2.31. Older versions echo the unknown flag
	// back as output instead of failing, so accept only a single absolute
	// path and otherwise retry without it.
	out, err := revParse(ctx, dir, "--path-format=absolute", "--git-common-dir")
	if err != nil || strings.Contains(out, "\n") || !filepath.IsAbs(out) {
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
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "rev-parse"}, args...)...)
	cmd.Env = discoveryEnv()
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
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
