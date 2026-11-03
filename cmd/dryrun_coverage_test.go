package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// dryRunSweep is the e2e script that runs every command under --dry-run
// between a snapshot and an unchanged check.
const dryRunSweep = "../e2e/testdata/dry_run_sweep.txtar"

// checked matches the sweep's check that nothing changed.
var checked = regexp.MustCompile(`(?m)^\s*unchanged\s*$`)

// dryRunExempt lists commands the sweep does not run, each with the reason it
// cannot or need not. Adding an entry needs the operator's approval.
var dryRunExempt = map[string]string{
	// Handlers that other tools invoke with a JSON payload; they never pass
	// --dry-run, so there is no preview to keep honest.
	"claude hook-worktree-create": "invoked by Claude Code's WorktreeCreate hook",
	"claude hook-worktree-remove": "invoked by Claude Code's WorktreeRemove hook",
	"herdr worktree-created":      "invoked by herdr's worktree.created event",
	"herdr worktree-removed":      "invoked by herdr's worktree.removed event",
	"herdr open-remove":           "invoked by herdr's remove action",
	"herdr remove":                "invoked by herdr's remove overlay pane",
	"_run-setup":                  "spawned by wtx add for background setup, which a dry run never starts",
}

// TestEveryCommandInDryRunSweep fails when a command is added without being
// run in the dry-run sweep, so --dry-run cannot silently start changing files.
func TestEveryCommandInDryRunSweep(t *testing.T) {
	script, err := os.ReadFile(filepath.FromSlash(dryRunSweep))
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if !c.Runnable() || c == rootCmd {
			return
		}
		path := strings.TrimPrefix(c.CommandPath(), rootCmd.Name()+" ")
		seen[path] = true
		if _, ok := dryRunExempt[path]; ok {
			return
		}
		run := regexp.MustCompile(`(?m)^\s*(! )?exec wtx --dry-run ` + regexp.QuoteMeta(path) + `(\s|$)`)
		loc := run.FindIndex(script)
		if len(loc) < 2 || !checked.Match(script[loc[1]:]) {
			t.Errorf("'wtx %s' is not run in %s before an `unchanged` check: add `exec wtx --dry-run %s ...` followed by `unchanged`, or exempt it in dryRunExempt with a reason", path, dryRunSweep, path)
		}
	}
	walk(rootCmd)

	for path := range dryRunExempt {
		if !seen[path] {
			t.Errorf("dryRunExempt lists %q, which is no longer a command; delete the entry", path)
		}
	}
}
