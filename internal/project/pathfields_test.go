package project

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{filepath.Join("internal", "project"), filepath.Join("internal", "config")}
	err = filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(moduleRoot, path)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			for _, dir := range allowed {
				if rel == dir {
					return filepath.SkipDir
				}
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
		for i, line := range strings.Split(string(data), "\n") {
			if pathFieldWrite.MatchString(line) {
				t.Errorf("%s:%d writes a config path field; use SetWorktreeDir/SetSharedDir: %s", rel, i+1, strings.TrimSpace(line))
			}
			if clonePathRead.MatchString(line) {
				t.Errorf("%s:%d reads a path spelling from Clone.Config(); use the Clone's accessor: %s", rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
