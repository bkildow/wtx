package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/project"
	"github.com/spf13/cobra"
)

// runJSON runs a command's RunE with --json and returns its stdout. It fails
// the test if anything reaches ui output.
func runJSON(t *testing.T, newCmd func() *cobra.Command) string {
	t.Helper()
	ui := captureUI(t)
	command := newCmd()
	command.SetContext(context.Background())
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	if err := command.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := command.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	if ui.Len() != 0 {
		t.Errorf("JSON mode wrote human output:\n%s", ui)
	}
	return stdout.String()
}

// decodeJSON decodes exactly one JSON document from out into v.
func decodeJSON(t *testing.T, out string, v any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(out))
	if err := decoder.Decode(v); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if decoder.More() {
		t.Fatalf("more than one JSON document on stdout:\n%s", out)
	}
}

// samePath reports whether got is absolute and names the same file as want,
// resolving symlinks such as macOS's /var -> /private/var.
func samePath(t *testing.T, got, want string) bool {
	t.Helper()
	if !filepath.IsAbs(got) {
		t.Errorf("path %q is not absolute", got)
	}
	return project.SamePath(got, want)
}

func TestListJSONCheckoutLayout(t *testing.T) {
	worktree := newSetupProject(t)
	root := filepath.Dir(filepath.Dir(worktree))

	var report listReport
	decodeJSON(t, runJSON(t, newListCmd), &report)

	if report.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", report.SchemaVersion)
	}
	if !samePath(t, report.ProjectRoot, root) {
		t.Errorf("project_root = %q, want %q", report.ProjectRoot, root)
	}
	if len(report.Worktrees) != 2 {
		t.Fatalf("got %d worktrees, want 2: %+v", len(report.Worktrees), report.Worktrees)
	}
	main, feature := report.Worktrees[0], report.Worktrees[1]
	if !main.ProjectRoot || !samePath(t, main.Path, root) || main.Branch == "" {
		t.Errorf("project root entry = %+v", main)
	}
	if feature.ProjectRoot || feature.Branch != "feature" || !samePath(t, feature.Path, worktree) {
		t.Errorf("feature entry = %+v", feature)
	}
	if len(feature.Head) != 40 {
		t.Errorf("head %q should be the full hash", feature.Head)
	}
}

func TestListJSONBareLayout(t *testing.T) {
	listingProject(t)

	var report listReport
	decodeJSON(t, runJSON(t, newListCmd), &report)

	branches := map[string]bool{}
	for _, wt := range report.Worktrees {
		if wt.ProjectRoot {
			t.Errorf("bare layout has a project root entry: %+v", wt)
		}
		if !filepath.IsAbs(wt.Path) {
			t.Errorf("path %q is not absolute", wt.Path)
		}
		branches[wt.Branch] = true
	}
	if len(report.Worktrees) != 2 || !branches["main"] || !branches["feature"] {
		t.Errorf("worktrees = %+v, want main and feature", report.Worktrees)
	}
}

func TestListJSONEmpty(t *testing.T) {
	listingProject(t)
	feature, _ := os.Getwd()
	root := filepath.Dir(filepath.Dir(feature))
	t.Chdir(root)
	for _, name := range []string{"main", "feature"} {
		path := filepath.Join(root, "worktrees", name)
		if out, err := exec.Command("git", "--git-dir", filepath.Join(root, ".bare"), "worktree", "remove", "--force", path).CombinedOutput(); err != nil {
			t.Fatalf("git worktree remove: %v\n%s", err, out)
		}
	}

	out := runJSON(t, newListCmd)
	if !strings.Contains(out, `"worktrees": []`) {
		t.Errorf("empty project should give an empty array:\n%s", out)
	}
	var report listReport
	decodeJSON(t, out, &report)
}
