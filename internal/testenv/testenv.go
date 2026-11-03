// Package testenv keeps a test binary off the machine it runs on: no real
// home directory, ~/.wtx, git config or enclosing wtx or git environment.
// Every package with tests calls Main from its TestMain; the coverage test in
// this package fails for one that does not.
package testenv

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// homes are the per-user directories Isolate redirects, by variable, to the
// subdirectory of its temp dir named here.
var homes = map[string]string{"HOME": "home", "WTX_HOME": "wtx-home", "XDG_CONFIG_HOME": "xdg"}

// GitVars is the git environment every test runs with: no system config, a
// fixed identity, no background maintenance (its lock files race file
// snapshots) and no optional locks, so read-only commands such as git status
// leave the index alone. e2e scripts start from an empty environment and set
// these themselves.
func GitVars() []string {
	return []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=maintenance.auto",
		"GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=gc.auto",
		"GIT_CONFIG_VALUE_1=0",
	}
}

// Isolate points HOME, WTX_HOME and XDG_CONFIG_HOME at fresh directories
// under a new temporary directory, which it returns. It clears every GIT_*,
// WTX_* and WT_* variable the caller inherited (a test run from a git hook
// or a wtx run script would otherwise see that repository or worktree), then
// sets GitVars and an empty global git config.
func Isolate() (string, error) {
	dir, err := os.MkdirTemp("", "wtx-test-")
	if err != nil {
		return "", err
	}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") || strings.HasPrefix(name, "WTX_") || strings.HasPrefix(name, "WT_") {
			_ = os.Unsetenv(name)
		}
	}
	gitconfig := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(gitconfig, nil, 0o644); err != nil {
		return "", err
	}
	vars := append(GitVars(), "GIT_CONFIG_GLOBAL="+gitconfig)
	for name, sub := range homes {
		vars = append(vars, name+"="+filepath.Join(dir, sub))
	}
	for _, kv := range vars {
		name, value, _ := strings.Cut(kv, "=")
		if err := os.Setenv(name, value); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, homes["HOME"]), 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Main runs m under Isolate and exits with its result. A test that writes
// under the shared HOME, WTX_HOME or XDG_CONFIG_HOME fails the run: it was
// relying on the ambient home, which on a developer's machine is the real
// one. Such a test sets its own with t.Setenv("WTX_HOME", t.TempDir()).
func Main(m *testing.M) {
	dir, err := Isolate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "testenv:", err)
		os.Exit(1)
	}
	code := m.Run()
	if leaked := written(dir); len(leaked) > 0 && code == 0 {
		fmt.Fprintf(os.Stderr, "testenv: tests wrote under the shared home directory; give each such test its own with t.Setenv(\"HOME\" or \"WTX_HOME\", t.TempDir()):\n  %s\n",
			strings.Join(leaked, "\n  "))
		code = 1
	}
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// written lists what exists under dir's home directories beyond the empty
// HOME that Isolate creates.
func written(dir string) []string {
	var leaked []string
	for _, sub := range homes {
		root := filepath.Join(dir, sub)
		_ = filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
			if err == nil && path != root {
				rel, _ := filepath.Rel(dir, path)
				leaked = append(leaked, rel)
			}
			return nil
		})
	}
	return leaked
}

// GoFiles calls fn with the module-relative path and contents of every .go
// file in the module, test files included, skipping dot directories and
// testdata. Source-scanning tests share it so they agree on what the module
// holds.
func GoFiles(t testing.TB, fn func(rel string, data []byte)) {
	t.Helper()
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("testenv: no go.mod above the test's directory")
		}
		root = parent
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		fn(filepath.ToSlash(rel), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
