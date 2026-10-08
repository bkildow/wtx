package doctor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
)

// inRepoFixture builds a wtx init --in-repo project: worktrees, shared files
// and a refresh script under .worktrees/, with worktrees "a" (dirty) and
// "feat/x" (nested) carrying managed shared symlinks.
func inRepoFixture(t *testing.T) (root, gitDir, wtxHome string) {
	t.Helper()
	t.Setenv("HOME", ui.CanonicalPath(t.TempDir()))
	wtxHome = ui.CanonicalPath(t.TempDir())
	t.Setenv("WTX_HOME", wtxHome)
	root, gitDir, _ = fixture(t, false)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.WorktreeDir = ".worktrees"
	cfg.SharedDir = ".worktrees/shared"
	cfg.Scripts = map[string]string{"check": "bin/check", "refresh": ".worktrees/bin/refresh"}
	if err := config.WriteAnnotatedWithValues(root, cfg); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, ".worktrees", "shared", "copy", ".env"), "SECRET=1\n", 0o600)
	write(t, filepath.Join(root, ".worktrees", "shared", "symlink", "notes.txt"), "shared notes\n", 0o644)
	write(t, filepath.Join(root, ".worktrees", "bin", "refresh"), "#!/bin/sh\nexit 0\n", 0o755)
	for _, branch := range []string{"a", "feat/x"} {
		wt := filepath.Join(root, ".worktrees", branch)
		gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", branch, wt)
		if err := os.Symlink(filepath.Join(root, ".worktrees", "shared", "symlink", "notes.txt"), filepath.Join(wt, "notes.txt")); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, ".worktrees", "a", "dirty.txt"), "uncommitted\n", 0o644)
	return root, gitDir, wtxHome
}

func gitOutput(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func migrateOutcomes(r Report) []RepairOutcome {
	var out []RepairOutcome
	for _, o := range r.Repairs {
		if o.ID == migrateID {
			out = append(out, o)
		}
	}
	return out
}

func TestMigrateHome(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root, gitDir, wtxHome := inRepoFixture(t)
	ctx := context.Background()
	home := filepath.Join(wtxHome, "proj")

	// Dry run lists every step and changes nothing.
	before := files(t, root)
	r := Run(ctx, Options{StartDir: root, MigrateHome: true, HomeName: "proj", DryRun: true})
	outcomes := migrateOutcomes(r)
	var actions []string
	for _, o := range outcomes {
		if o.Status != Planned {
			t.Fatalf("dry run applied a step: %+v", o)
		}
		actions = append(actions, o.Action)
	}
	joined := strings.Join(actions, "\n")
	for _, want := range []string{"write ownership marker", "move .worktrees/shared", "move .worktrees/bin", "git worktree move .worktrees/a", "git worktree move .worktrees/feat/x", "retarget shared symlinks", "git worktree repair", "remove .worktrees if empty", "set git config wtx.name proj", "remove worktree_dir, shared_dir"} {
		if !strings.Contains(joined, want) {
			t.Errorf("dry run missing step %q in:\n%s", want, joined)
		}
	}
	if !reflect.DeepEqual(before, files(t, root)) {
		t.Fatal("dry run changed the project")
	}
	if entries, _ := os.ReadDir(wtxHome); len(entries) != 0 {
		t.Fatal("dry run wrote to WTX_HOME")
	}

	// Migrate.
	r = Run(ctx, Options{StartDir: root, MigrateHome: true, HomeName: "proj"})
	if r.Unsuccessful(false) {
		t.Fatalf("migration failed: repairs=%+v findings=%+v", r.Repairs, r.Findings)
	}
	list := gitOutput(t, "--git-dir", gitDir, "worktree", "list", "--porcelain")
	for _, branch := range []string{"a", "feat/x"} {
		if !strings.Contains(list, "worktree "+filepath.Join(home, "worktrees", branch)+"\n") {
			t.Errorf("worktree %s not at new path:\n%s", branch, list)
		}
	}
	if status := gitOutput(t, "-C", filepath.Join(home, "worktrees", "a"), "status", "--porcelain"); !strings.Contains(status, "?? dirty.txt") {
		t.Errorf("dirty file lost: %q", status)
	}
	if data, err := os.ReadFile(filepath.Join(home, "shared", "copy", ".env")); err != nil || string(data) != "SECRET=1\n" {
		t.Errorf("shared copy file: %q %v", data, err)
	}
	if info, err := os.Stat(filepath.Join(home, "bin", "refresh")); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("refresh script: %v %v", info, err)
	}
	for _, branch := range []string{"a", "feat/x"} {
		link := filepath.Join(home, "worktrees", branch, "notes.txt")
		target, err := os.Readlink(link)
		if err != nil || target != filepath.Join(home, "shared", "symlink", "notes.txt") {
			t.Errorf("symlink %s -> %q (%v)", link, target, err)
		}
		if data, err := os.ReadFile(link); err != nil || string(data) != "shared notes\n" {
			t.Errorf("symlink %s does not resolve: %v", link, err)
		}
	}
	cfgData, err := os.ReadFile(filepath.Join(root, config.ConfigFileName))
	if err != nil {
		t.Fatal(err)
	}
	// The config keeps no machine-local paths: worktree_dir, shared_dir and
	// the refresh entry (found in bin/ by name) are gone, and wtx.name
	// records the clone's directory.
	for _, want := range []string{"check: bin/check\n", "# Directory for worktrees"} {
		if !strings.Contains(string(cfgData), want) {
			t.Errorf("config missing %q:\n%s", want, cfgData)
		}
	}
	for _, unwanted := range []string{"\nworktree_dir:", "\nshared_dir:", "refresh:", "~/.wtx/proj"} {
		if strings.Contains(string(cfgData), unwanted) {
			t.Errorf("config still has %q:\n%s", unwanted, cfgData)
		}
	}
	if name := gitOutput(t, "--git-dir", gitDir, "config", "--local", "wtx.name"); strings.TrimSpace(name) != "proj" {
		t.Errorf("wtx.name = %q", name)
	}
	if m, err := project.ReadMarker(home); err != nil || !project.SamePath(m.Root, root) {
		t.Errorf("marker: %+v %v", m, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".worktrees")); !os.IsNotExist(err) {
		t.Errorf(".worktrees not removed: %v", err)
	}
	backups, _ := filepath.Glob(filepath.Join(gitDir, backupDirName, config.ConfigFileName+".wtx-backup-*"))
	if len(backups) != 1 {
		t.Errorf("config backup: %v", backups)
	}
	if f := finding(r, migrateID, ""); f == nil || f.Severity != OK {
		t.Errorf("post-migration finding: %+v", f)
	}

	// Re-run is a no-op.
	before = files(t, root)
	homeBefore := files(t, home)
	r = Run(ctx, Options{StartDir: root, MigrateHome: true, HomeName: "proj"})
	if len(r.Repairs) != 0 || r.Unsuccessful(false) {
		t.Fatalf("re-run not a no-op: %+v", r.Repairs)
	}
	if !reflect.DeepEqual(before, files(t, root)) || !reflect.DeepEqual(homeBefore, files(t, home)) {
		t.Fatal("re-run changed files")
	}
}

func TestMigrateHomeResumesAfterSkippedWorktree(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root, gitDir, wtxHome := inRepoFixture(t)
	ctx := context.Background()
	locked := filepath.Join(root, ".worktrees", "feat", "x")
	gitRun(t, "--git-dir", gitDir, "worktree", "lock", locked)

	r := Run(ctx, Options{StartDir: root, MigrateHome: true, HomeName: "proj"})
	if r.Unsuccessful(false) {
		t.Fatalf("migration failed: %+v", r.Repairs)
	}
	if !exists(filepath.Join(wtxHome, "proj", "worktrees", "a", ".git")) {
		t.Fatal("unlocked worktree not moved")
	}
	if !exists(filepath.Join(locked, ".git")) {
		t.Fatal("locked worktree moved")
	}
	// Config moved on; the leftover is reported as a layout warning.
	if f := finding(r, "home.layout", locked); f == nil || f.Severity != Warn {
		t.Fatalf("leftover not reported: %+v", f)
	}

	gitRun(t, "--git-dir", gitDir, "worktree", "unlock", locked)
	r = Run(ctx, Options{StartDir: root, MigrateHome: true})
	if r.Unsuccessful(false) {
		t.Fatalf("resume failed: %+v", r.Repairs)
	}
	if !exists(filepath.Join(wtxHome, "proj", "worktrees", "feat", "x", ".git")) {
		t.Fatal("resumed worktree not moved")
	}
	if _, err := os.Stat(filepath.Join(root, ".worktrees")); !os.IsNotExist(err) {
		t.Fatal(".worktrees not removed after resume")
	}
	if target, _ := os.Readlink(filepath.Join(wtxHome, "proj", "worktrees", "feat", "x", "notes.txt")); target != filepath.Join(wtxHome, "proj", "shared", "symlink", "notes.txt") {
		t.Fatalf("leftover symlink not retargeted: %q", target)
	}
}

// A clone whose config already leaves the paths per clone (a teammate who
// pulled a migrated .worktree.yml and ran wtx init) still has its own
// .worktrees/: shared files and bin move with the worktrees, into the
// empty scaffold wtx init created.
func TestMigrateHomeLeftoverClone(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	root, gitDir, wtxHome := inRepoFixture(t)
	ctx := context.Background()
	home := filepath.Join(wtxHome, "proj")
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.WorktreeDirSet, cfg.SharedDirSet = false, false
	cfg.Scripts = map[string]string{"check": "bin/check"}
	if err := config.WriteAnnotatedWithValues(root, cfg); err != nil {
		t.Fatal(err)
	}
	gitRun(t, "--git-dir", gitDir, "config", "--local", project.NameConfigKey, "proj")
	for _, dir := range []string{"shared/copy", "shared/symlink"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := project.WriteMarker(home, root, false); err != nil {
		t.Fatal(err)
	}

	r := Run(ctx, Options{StartDir: root, MigrateHome: true})
	if r.Unsuccessful(false) {
		t.Fatalf("migration failed: repairs=%+v findings=%+v", r.Repairs, r.Findings)
	}
	if data, err := os.ReadFile(filepath.Join(home, "shared", "copy", ".env")); err != nil || string(data) != "SECRET=1\n" {
		t.Errorf("shared copy file: %q %v", data, err)
	}
	if !exists(filepath.Join(home, "bin", "refresh")) {
		t.Error("bin not moved")
	}
	link := filepath.Join(home, "worktrees", "a", "notes.txt")
	if target, err := os.Readlink(link); err != nil || target != filepath.Join(home, "shared", "symlink", "notes.txt") {
		t.Errorf("symlink %s -> %q (%v)", link, target, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".worktrees")); !os.IsNotExist(err) {
		t.Errorf(".worktrees not removed: %v", err)
	}
}

func TestMigrateHomeRefusals(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx := context.Background()

	t.Run("name collision", func(t *testing.T) {
		root, _, wtxHome := inRepoFixture(t)
		other := t.TempDir()
		if err := config.WriteAnnotated(other); err != nil {
			t.Fatal(err)
		}
		if err := project.WriteMarker(filepath.Join(wtxHome, filepath.Base(root)), other, false); err != nil {
			t.Fatal(err)
		}
		before := files(t, root)
		r := Run(ctx, Options{StartDir: root, MigrateHome: true})
		f := finding(r, migrateID, "")
		if f == nil || f.Severity != Fail || !strings.Contains(f.Remedy, "--name") || len(r.Repairs) != 0 {
			t.Fatalf("collision: %+v %+v", f, r.Repairs)
		}
		if !reflect.DeepEqual(before, files(t, root)) {
			t.Fatal("collision changed the project")
		}
	})

	t.Run("bare project", func(t *testing.T) {
		t.Setenv("WTX_HOME", t.TempDir())
		root, _, _ := fixture(t, true)
		r := Run(ctx, Options{StartDir: root, MigrateHome: true})
		if f := finding(r, migrateID, ""); f == nil || f.Severity != Warn || len(r.Repairs) != 0 {
			t.Fatalf("bare: %+v %+v", f, r.Repairs)
		}
	})

	t.Run("tracked shared files stay", func(t *testing.T) {
		root, _, wtxHome := inRepoFixture(t)
		gitRun(t, "-C", root, "add", "-f", ".worktrees/shared/copy/.env")
		r := Run(ctx, Options{StartDir: root, MigrateHome: true, HomeName: "proj"})
		if r.Unsuccessful(false) {
			t.Fatalf("migration failed: %+v", r.Repairs)
		}
		if !exists(filepath.Join(root, ".worktrees", "shared", "copy", ".env")) || exists(filepath.Join(wtxHome, "proj", "shared")) {
			t.Fatal("tracked shared directory moved")
		}
		if exists(filepath.Join(wtxHome, "proj", "bin")) {
			t.Fatal("bin moved away from the shared directory it is resolved against")
		}
		// worktree_dir is left to the per-clone default; shared_dir stays.
		cfg, err := project.LoadConfig(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.WorktreeDirSet || cfg.WorktreeDir != "~/.wtx/proj/worktrees" || cfg.SharedDir != ".worktrees/shared" {
			t.Fatalf("config: %+v", cfg)
		}
	})
}

// copyTree is the cross-device fallback of moveDir; rename never hits it
// within one temp filesystem, so exercise it directly.
func TestCopyTreeAndRemoveEmptyDirs(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "out")
	write(t, filepath.Join(src, "copy", ".env"), "x", 0o600)
	write(t, filepath.Join(src, "bin", "run"), "#!/bin/sh\n", 0o755)
	if err := os.Symlink("../copy/.env", filepath.Join(src, "bin", "link")); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dst, "bin", "run")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("mode: %v %v", info, err)
	}
	if target, _ := os.Readlink(filepath.Join(dst, "bin", "link")); target != "../copy/.env" {
		t.Fatalf("symlink: %q", target)
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "keep", "file"), "x", 0o600)
	if err := removeEmptyDirs(dir); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, "a")) || !exists(filepath.Join(dir, "keep", "file")) {
		t.Fatal("removeEmptyDirs removed the wrong things")
	}
}

func TestRewriteConfigPreservesLayout(t *testing.T) {
	data := []byte("# top\nversion: 1\ngit_dir: .git\n# wt\nworktree_dir: .worktrees # old\nshared_dir: .worktrees/shared\n\nscripts:\n  # keep\n  refresh: .worktrees/bin/refresh\n  seed: .worktrees/bin/seed.sh\n  \"odd name\": bin/x\n")
	want, err := config.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	want.WorktreeDirSet, want.SharedDirSet = false, false
	want.Scripts = map[string]string{"seed": "~/.wtx/p/bin/seed.sh", "odd name": "bin/x"}
	out, err := rewriteConfig(data, want)
	if err != nil {
		t.Fatal(err)
	}
	expected := "# top\nversion: 1\ngit_dir: .git\n# wt\n\nscripts:\n  # keep\n  seed: ~/.wtx/p/bin/seed.sh\n  \"odd name\": bin/x\n"
	if string(out) != expected {
		t.Fatalf("got:\n%s", out)
	}

	// A scripts key left without entries is dropped.
	data = []byte("git_dir: .git\nworktree_dir: .worktrees\nscripts:\n  refresh: .worktrees/bin/refresh\nmain_branch: main\n")
	want = &config.Config{GitDir: ".git", MainBranch: "main"}
	if out, err := rewriteConfig(data, want); err != nil || string(out) != "git_dir: .git\nmain_branch: main\n" {
		t.Fatalf("got %q, %v", out, err)
	}

	if _, err := rewriteConfig([]byte("worktree_dir: .worktrees\nshared_dir: .worktrees/shared\nscripts: {refresh: .worktrees/bin/refresh, odd name: bin/x}\n"), &config.Config{Scripts: map[string]string{"odd name": "bin/x"}}); err == nil {
		t.Fatal("flow-style scripts must be refused")
	}
}
