package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bkildow/wtx/internal/ui"
)

// WorktreeIncludeFile is the gitignore-syntax file at the root of the main
// worktree listing ignored files to copy into new worktrees. The name and
// semantics are shared with Claude Code and Conductor.
const WorktreeIncludeFile = ".worktreeinclude"

// ListWorktreeIncludes returns the paths (relative to mainWorktree) of files
// that match a pattern in mainWorktree/.worktreeinclude AND are ignored by
// git. Tracked files are never returned. It returns nil without error when
// the .worktreeinclude file does not exist.
//
// Unlike Runner methods it does not pass --git-dir: in a bare layout the
// main worktree has its own index under .bare/worktrees/<name>/, and git
// only finds it when discovering the repository from the worktree itself.
func ListWorktreeIncludes(ctx context.Context, mainWorktree string) ([]string, error) {
	includeFile := filepath.Join(mainWorktree, WorktreeIncludeFile)
	if _, err := os.Stat(includeFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	ignored, err := lsFilesInWorktree(ctx, mainWorktree,
		"ls-files", "-z", "--others", "--ignored", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	// Without --exclude-standard, "ignored" here means "matched by
	// .worktreeinclude"; .gitignore is not consulted for this set.
	matched, err := lsFilesInWorktree(ctx, mainWorktree,
		"ls-files", "-z", "--others", "--ignored", "--exclude-from="+includeFile)
	if err != nil {
		return nil, err
	}

	return intersectPaths(parseNULList(ignored), parseNULList(matched)), nil
}

func lsFilesInWorktree(ctx context.Context, dir string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", dir}, args...)
	cmdStr := "git " + strings.Join(fullArgs, " ")

	ui.Command(cmdStr)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w\n%s", cmdStr, err, stderr.String())
	}
	return stdout.String(), nil
}

// parseNULList splits NUL-delimited git output (ls-files -z) into paths,
// dropping empty entries.
func parseNULList(output string) []string {
	var paths []string
	for _, p := range strings.Split(output, "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// intersectPaths returns the sorted paths present in both lists. Entries
// ending in "/" are directories git refused to descend into (nested
// repositories such as worktrees under the project root) and are dropped.
func intersectPaths(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, p := range b {
		inB[p] = true
	}
	seen := make(map[string]bool)
	var out []string
	for _, p := range a {
		if strings.HasSuffix(p, "/") || !inB[p] || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
