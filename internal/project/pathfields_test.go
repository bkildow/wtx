package project

import (
	"regexp"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/testenv"
)

// pathFieldWrite matches an assignment to worktree_dir or shared_dir, alone
// or in a tuple assignment.
var pathFieldWrite = regexp.MustCompile(`\.(WorktreeDir|SharedDir)(\s*,[^=\n]*)?\s*=[^=]`)

// clonePathRead matches reading a path spelling from a Clone's config, where
// the Clone's own accessor (WorktreesDir, SharedDir, GitDir) gives the
// resolved directory.
var clonePathRead = regexp.MustCompile(`\.Config\(\)\.(WorktreeDir|SharedDir|GitDir)\b`)

// TestPathFieldsWrittenOnlyInProject keeps writes to Config.WorktreeDir and
// Config.SharedDir inside the config and project packages: everyone else
// records an explicit path with Config.SetWorktreeDir/SetSharedDir and reads
// resolved directories from a Clone, never the spellings in Clone.Config().
// Test fixtures may build configs freely.
//
// Other reads of the spellings are allowed: a regular expression cannot tell
// a Config from a ConfigPaths, and doctor compares the spellings of a
// rewritten .worktree.yml with the ones it meant to write.
func TestPathFieldsWrittenOnlyInProject(t *testing.T) {
	testenv.GoFiles(t, func(rel string, data []byte) {
		if strings.HasSuffix(rel, "_test.go") || strings.HasPrefix(rel, "internal/project/") || strings.HasPrefix(rel, "internal/config/") {
			return
		}
		for i, line := range strings.Split(string(data), "\n") {
			if pathFieldWrite.MatchString(line) {
				t.Errorf("%s:%d writes a config path field; use SetWorktreeDir/SetSharedDir: %s", rel, i+1, strings.TrimSpace(line))
			}
			if clonePathRead.MatchString(line) {
				t.Errorf("%s:%d reads a path spelling from Clone.Config(); use the Clone's accessor: %s", rel, i+1, strings.TrimSpace(line))
			}
		}
	})
}
