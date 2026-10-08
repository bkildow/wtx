package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
)

// homeFixture builds an init-layout project whose worktrees and shared files
// live in $WTX_HOME/proj, with one linked worktree there. HOME and WTX_HOME
// point at temporary directories.
func homeFixture(t *testing.T) (root, wtxHome, worktree string) {
	t.Helper()
	t.Setenv("HOME", ui.CanonicalPath(t.TempDir()))
	wtxHome = ui.CanonicalPath(t.TempDir())
	t.Setenv("WTX_HOME", wtxHome)
	root, gitDir, _ := fixture(t, false)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.WorktreeDir = "~/.wtx/proj/worktrees"
	cfg.SharedDir = "~/.wtx/proj/shared"
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	if err := project.WriteMarker(filepath.Join(wtxHome, "proj"), root, false); err != nil {
		t.Fatal(err)
	}
	worktree = filepath.Join(wtxHome, "proj", "worktrees", "feat")
	gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", "feat", worktree)
	return root, wtxHome, worktree
}

func homeFindings(r Report) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if strings.HasPrefix(f.ID, "home.") {
			out = append(out, f)
		}
	}
	return out
}

func TestHomeProjectHealthy(t *testing.T) {
	root, _, worktree := homeFixture(t)
	r := Run(context.Background(), Options{StartDir: root})
	for _, f := range homeFindings(r) {
		if f.Severity != OK {
			t.Errorf("unexpected home finding: %+v", f)
		}
	}
	for _, id := range []string{"home.paths", "home.marker", "home.layout", "home.orphans"} {
		if finding(r, id, "") == nil {
			t.Errorf("missing %s finding", id)
		}
	}
	if f := finding(r, "git.worktrees", worktree); f == nil || f.Severity != OK {
		t.Fatalf("external worktree not inspected: %+v", f)
	}
}

func TestExternalWorktreeIsManaged(t *testing.T) {
	root, _, worktree := homeFixture(t)
	settings := filepath.Join(worktree, ".claude", "settings.json")
	write(t, settings, `{}`, 0o600)
	state := filepath.Join(worktree, project.SetupStateFile)
	write(t, state, `{"status":"running","pid":2147483647,"hooks_total":1}`, 0o600)
	r := Run(context.Background(), Options{StartDir: root})
	for _, f := range r.Findings {
		if strings.Contains(f.Explanation, "outside the project") || strings.Contains(f.Remedy, "outside the project") {
			t.Errorf("external worktree flagged as outside the project: %+v", f)
		}
	}
	if f := finding(r, "setup.state", state); f == nil || !f.Repairable {
		t.Fatalf("setup state in external worktree must be repairable: %+v", f)
	}
}

func TestHomeMarkerChecks(t *testing.T) {
	root, wtxHome, _ := homeFixture(t)
	dir := filepath.Join(wtxHome, "proj")
	marker := filepath.Join(dir, project.MarkerFileName)

	// Missing marker: warn, repaired by --fix.
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	r := Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "home.marker", marker); f == nil || f.Severity != Warn || !f.Repairable {
		t.Fatalf("missing marker: %+v", f)
	}
	r = Run(context.Background(), Options{StartDir: root, Fix: true})
	if r.Unsuccessful(false) {
		t.Fatalf("marker repair failed: %+v", r.Repairs)
	}
	if m, err := project.ReadMarker(dir); err != nil || !project.SamePath(m.Root, root) {
		t.Fatalf("marker not written: %+v %v", m, err)
	}

	// Stale owner (moved repository): repairable.
	if err := project.WriteMarker(dir, filepath.Join(t.TempDir(), "gone"), false); err != nil {
		t.Fatal(err)
	}
	r = Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "home.marker", marker); f == nil || f.Severity != Warn || !f.Repairable {
		t.Fatalf("stale marker: %+v", f)
	}

	// Another live project owns it: never repairable.
	other := t.TempDir()
	if err := config.WriteAnnotated(other); err != nil {
		t.Fatal(err)
	}
	if err := project.WriteMarker(dir, other, false); err != nil {
		t.Fatal(err)
	}
	r = Run(context.Background(), Options{StartDir: root, Fix: true})
	if f := finding(r, "home.marker", marker); f == nil || f.Severity != Warn || f.Repairable {
		t.Fatalf("foreign marker: %+v", f)
	}
	if m, _ := project.ReadMarker(dir); !project.SamePath(m.Root, other) {
		t.Fatal("--fix changed another project's marker")
	}
}

func TestHomeMarkerPerClone(t *testing.T) {
	t.Setenv("HOME", ui.CanonicalPath(t.TempDir()))
	wtxHome := ui.CanonicalPath(t.TempDir())
	t.Setenv("WTX_HOME", wtxHome)
	root, gitDir, _ := fixture(t, false)
	write(t, filepath.Join(root, config.ConfigFileName), "git_dir: .git\ndisk_warn: false\n", 0o600)
	dir := filepath.Join(wtxHome, filepath.Base(root))
	marker := filepath.Join(dir, project.MarkerFileName)

	// Not set up yet: warn and point at wtx init; the name is the directory name.
	r := Run(context.Background(), Options{StartDir: root})
	f := finding(r, "home.marker", marker)
	if f == nil || f.Severity != Warn || !strings.Contains(f.Remedy, "wtx init") || !strings.Contains(f.Explanation, "repository directory name") {
		t.Fatalf("clone not set up: %+v", f)
	}

	// wtx.name moves the directory; a missing marker in an existing
	// directory is repaired by --fix and the detail names wtx.name.
	gitRun(t, "--git-dir", gitDir, "config", "--local", project.NameConfigKey, "teammate")
	dir = filepath.Join(wtxHome, "teammate")
	marker = filepath.Join(dir, project.MarkerFileName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r = Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "home.marker", marker); f == nil || f.Severity != Warn || !f.Repairable || !strings.Contains(f.Explanation, "git config wtx.name") {
		t.Fatalf("missing marker: %+v", f)
	}
	if r = Run(context.Background(), Options{StartDir: root, Fix: true}); r.Unsuccessful(false) {
		t.Fatalf("marker repair failed: %+v", r.Repairs)
	}
	r = Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "home.marker", marker); f == nil || f.Severity != OK || !strings.Contains(f.Explanation, `"teammate" from git config wtx.name`) {
		t.Fatalf("repaired marker: %+v", f)
	}

	// Another live project owns the directory: suggest wtx init --name.
	other := t.TempDir()
	if err := config.WriteAnnotated(other); err != nil {
		t.Fatal(err)
	}
	if err := project.WriteMarker(dir, other, false); err != nil {
		t.Fatal(err)
	}
	r = Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "home.marker", marker); f == nil || f.Severity != Warn || !strings.Contains(f.Remedy, "wtx init --name") {
		t.Fatalf("foreign marker: %+v", f)
	}
}

func TestHomeOrphans(t *testing.T) {
	root, wtxHome, _ := homeFixture(t)
	orphan := filepath.Join(wtxHome, "old")
	if err := project.WriteMarker(orphan, filepath.Join(t.TempDir(), "deleted-repo"), false); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(orphan, "shared", "copy", ".env"), "x", 0o600)
	// Live projects and directories without a marker are not orphans.
	live := t.TempDir()
	if err := config.WriteAnnotated(live); err != nil {
		t.Fatal(err)
	}
	if err := project.WriteMarker(filepath.Join(wtxHome, "live"), live, false); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wtxHome, "unrelated"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := Run(context.Background(), Options{StartDir: root, Fix: true})
	orphans := findings(r, "home.orphans")
	if len(orphans) != 1 || orphans[0].Path != orphan || orphans[0].Severity != Warn || orphans[0].Repairable {
		t.Fatalf("orphans: %+v", orphans)
	}
	if _, err := os.Stat(filepath.Join(orphan, "shared", "copy", ".env")); err != nil {
		t.Fatal("orphan directory must never be deleted")
	}
}

func TestHomePathsUnexpandable(t *testing.T) {
	root, _, _ := homeFixture(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.WorktreeDir = "~/elsewhere/worktrees"
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "")
	r := Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "home.paths", ""); f == nil || f.Severity != Fail {
		t.Fatalf("unexpandable worktree_dir: %+v", f)
	}
}

func TestHomePathsNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permissions")
	}
	root, wtxHome, _ := homeFixture(t)
	dir := filepath.Join(wtxHome, "proj", "worktrees")
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // directory needs search permission
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) //nolint:gosec // restore a test directory
	r := Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "home.paths", dir); f == nil || f.Severity != Fail {
		t.Fatalf("read-only worktree dir: %+v", f)
	}
}

func TestHomeLayoutInRepoAndLeftovers(t *testing.T) {
	root, gitDir, _ := fixture(t, false)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.WorktreeDir = ".worktrees"
	cfg.SharedDir = ".worktrees/shared"
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	left := filepath.Join(root, ".worktrees", "old")
	gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", "old", left)

	r := Run(context.Background(), Options{StartDir: root})
	f := finding(r, "home.layout", "")
	if f == nil || f.Severity != OK || !strings.Contains(f.Remedy, "--migrate-home") {
		t.Fatalf("in-repo layout hint: %+v", f)
	}

	// worktree_dir moved, but a worktree was left behind in .worktrees.
	t.Setenv("WTX_HOME", t.TempDir())
	cfg.WorktreeDir = "~/.wtx/proj/worktrees"
	cfg.SharedDir = "~/.wtx/proj/shared"
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	r = Run(context.Background(), Options{StartDir: root})
	f = finding(r, "home.layout", left)
	if f == nil || f.Severity != Warn || f.Subject != "old" || !strings.Contains(f.Remedy, "--migrate-home") {
		t.Fatalf("leftover worktree: %+v", f)
	}
}
