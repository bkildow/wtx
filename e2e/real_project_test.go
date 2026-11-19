package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// realProject makes a git repository with one commit and runs the real wtx
// init in it, with WTX_HOME set to a new directory so ~/.wtx state stays out
// of the real home. It returns the repository and that home.
func realProject(t *testing.T, bin string) (root, wtxHome string) {
	t.Helper()
	root = t.TempDir()
	wtxHome = t.TempDir()
	t.Setenv("WTX_HOME", wtxHome)
	for _, args := range [][]string{{"init", "-b", "main", root}, {"-C", root, "commit", "--allow-empty", "-m", "initial"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	c := exec.Command(filepath.Join(bin, "wtx"), "init")
	c.Dir = root
	c.Env = realEnv(wtxHome)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("wtx init: %v: %s", err, out)
	}
	return root, wtxHome
}

// realEnv is the environment the real wtx runs in: this process's, with disk
// warnings off and WTX_HOME at wtxHome, plus extra.
func realEnv(wtxHome string, extra ...string) []string {
	return append(append(os.Environ(), "WTX_NO_DISK_WARN=1", "WTX_HOME="+wtxHome), extra...)
}
