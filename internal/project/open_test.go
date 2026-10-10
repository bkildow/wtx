package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/ui"
)

// layoutFixture is one Clone on disk: its Project root, the directory Open
// starts from, and the paths a resolved Clone should report.
type layoutFixture struct {
	root, start            string
	worktrees, shared, bin string
	cloneDir               string // "" when the Clone has no Clone dir
	main                   string // Main worktree path, "" when none
	managed                []string
	name                   CloneName
	layout                 LayoutKind
	gitDir                 string
}

// bareClone builds a Bare layout Clone: a bare repository at .bare with the
// main branch checked out at worktrees/main and a feature branch outside the
// worktrees directory (an External worktree).
func bareClone(t *testing.T, base string) layoutFixture {
	src := filepath.Join(base, "src")
	initRepo(t, src, false)
	root := filepath.Join(base, "proj")
	runGit(t, "clone", "--bare", src, filepath.Join(root, ".bare"))
	writeConfig(t, root, "version: 1\ngit_dir: .bare\n")
	gitDir := filepath.Join(root, ".bare")
	main := filepath.Join(root, "worktrees", "main")
	external := filepath.Join(base, "elsewhere", "feature")
	runGit(t, "--git-dir", gitDir, "worktree", "add", main, "main")
	runGit(t, "--git-dir", gitDir, "worktree", "add", "-b", "feature", external)
	return layoutFixture{
		root: root, start: main,
		worktrees: filepath.Join(root, "worktrees"),
		shared:    filepath.Join(root, "shared"),
		bin:       filepath.Join(root, "bin"),
		main:      main,
		managed:   []string{main, external},
		layout:    BareLayout,
		gitDir:    gitDir,
	}
}

// checkoutClone builds a Checkout layout Clone at base/myrepo whose
// .worktree.yml holds extra after git_dir, with wtx.name set to gitName when
// that is not empty. Callers fill in the expected paths, then addFeature.
func checkoutClone(t *testing.T, base, extra, gitName string) layoutFixture {
	root := filepath.Join(base, "myrepo")
	initRepo(t, root, false)
	if gitName != "" {
		runGit(t, "-C", root, "config", "--local", NameConfigKey, gitName)
	}
	writeConfig(t, root, "version: 1\ngit_dir: .git\n"+extra)
	return layoutFixture{root: root, start: root, main: root, layout: CheckoutLayout, gitDir: filepath.Join(root, ".git")}
}

// addFeature adds a linked worktree on a new branch feature under the
// expected worktrees directory: the Clone's only Managed worktree.
func (f *layoutFixture) addFeature(t *testing.T) {
	path := filepath.Join(f.worktrees, "feature")
	runGit(t, "--git-dir", f.gitDir, "worktree", "add", "-b", "feature", path)
	f.managed = []string{path}
}

func writeConfig(t *testing.T, root, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, config.ConfigFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenResolvesEachLayout(t *testing.T) {
	requireGit(t)

	tests := []struct {
		name  string
		build func(t *testing.T, base, home string) layoutFixture
	}{
		{"bare", func(t *testing.T, base, _ string) layoutFixture {
			return bareClone(t, base)
		}},
		{"checkout with clone dir", func(t *testing.T, base, home string) layoutFixture {
			f := checkoutClone(t, base, "", "")
			cloneDir := filepath.Join(home, ".wtx", "myrepo")
			f.cloneDir = cloneDir
			f.worktrees, f.shared, f.bin = filepath.Join(cloneDir, "worktrees"), filepath.Join(cloneDir, "shared"), filepath.Join(cloneDir, "bin")
			f.name = CloneName{Name: "myrepo", Source: NameFromDirectory}
			f.addFeature(t)
			return f
		}},
		{"checkout with wtx.name", func(t *testing.T, base, home string) layoutFixture {
			f := checkoutClone(t, base, "", "teammate")
			cloneDir := filepath.Join(home, ".wtx", "teammate")
			f.cloneDir = cloneDir
			f.worktrees, f.shared, f.bin = filepath.Join(cloneDir, "worktrees"), filepath.Join(cloneDir, "shared"), filepath.Join(cloneDir, "bin")
			f.name = CloneName{Name: "teammate", Source: NameFromGitConfig}
			f.addFeature(t)
			return f
		}},
		{"checkout under WTX_HOME", func(t *testing.T, base, home string) layoutFixture {
			wtxHome := filepath.Join(base, "custom-home")
			t.Setenv(WtxHomeEnv, wtxHome)
			f := checkoutClone(t, base, "", "")
			cloneDir := filepath.Join(wtxHome, "myrepo")
			f.cloneDir = cloneDir
			f.worktrees, f.shared, f.bin = filepath.Join(cloneDir, "worktrees"), filepath.Join(cloneDir, "shared"), filepath.Join(cloneDir, "bin")
			f.name = CloneName{Name: "myrepo", Source: NameFromDirectory}
			f.addFeature(t)
			return f
		}},
		{"in-repo", func(t *testing.T, base, _ string) layoutFixture {
			f := checkoutClone(t, base, "worktree_dir: .worktrees\nshared_dir: .worktrees/shared\n", "")
			f.layout = InRepoLayout
			f.worktrees = filepath.Join(f.root, ".worktrees")
			f.shared, f.bin = filepath.Join(f.worktrees, "shared"), filepath.Join(f.worktrees, "bin")
			f.addFeature(t)
			f.start = filepath.Join(f.worktrees, "feature")
			return f
		}},
		{"checkout with only shared_dir omitted", func(t *testing.T, base, home string) layoutFixture {
			f := checkoutClone(t, base, "worktree_dir: ~/trees\n", "")
			cloneDir := filepath.Join(home, ".wtx", "myrepo")
			f.cloneDir = cloneDir
			f.worktrees = filepath.Join(home, "trees")
			f.shared, f.bin = filepath.Join(cloneDir, "shared"), filepath.Join(cloneDir, "bin")
			f.name = CloneName{Name: "myrepo", Source: NameFromDirectory}
			f.addFeature(t)
			return f
		}},
		{"custom paths", func(t *testing.T, base, home string) layoutFixture {
			f := checkoutClone(t, base, "worktree_dir: ~/trees\nshared_dir: ~/.wtx/x/shared\n", "")
			f.worktrees = filepath.Join(home, "trees")
			f.shared, f.bin = filepath.Join(home, ".wtx", "x", "shared"), filepath.Join(home, ".wtx", "x", "bin")
			f.addFeature(t)
			return f
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := ui.CanonicalPath(t.TempDir())
			home := filepath.Join(base, "home")
			t.Setenv("HOME", home)
			t.Setenv(WtxHomeEnv, "")
			want := tt.build(t, base, home)
			ctx := context.Background()

			c, err := Open(ctx, want.start, Options{})
			if err != nil {
				t.Fatal(err)
			}
			assertClone(t, ctx, c, want)

			// Resolve on an already loaded config gives the same Clone.
			cfg, err := config.Load(want.root)
			if err != nil {
				t.Fatal(err)
			}
			r, err := Resolve(ctx, want.root, cfg, Options{})
			if err != nil {
				t.Fatal(err)
			}
			assertClone(t, ctx, r, want)
		})
	}
}

func assertClone(t *testing.T, ctx context.Context, c *Clone, want layoutFixture) {
	t.Helper()
	same := func(what, got, want string) {
		t.Helper()
		if (got == "") != (want == "") || (got != "" && ui.CanonicalPath(got) != ui.CanonicalPath(want)) {
			t.Errorf("%s = %q, want %q", what, got, want)
		}
	}
	same("Root", c.Root(), want.root)
	if c.Layout() != want.layout {
		t.Errorf("Layout = %v, want %v", c.Layout(), want.layout)
	}
	if c.Name() != want.name {
		t.Errorf("Name = %+v, want %+v", c.Name(), want.name)
	}
	dir, ok := c.CloneDir()
	if ok != (want.cloneDir != "") {
		t.Errorf("CloneDir ok = %v, want %v", ok, want.cloneDir != "")
	}
	same("CloneDir", dir, want.cloneDir)
	same("GitDir", c.GitDir(), want.gitDir)
	same("WorktreesDir", c.WorktreesDir(), want.worktrees)
	same("SharedDir", c.SharedDir(), want.shared)
	same("BinDir", c.BinDir(), want.bin)
	if c.Config() == nil || c.Runner() == nil {
		t.Fatal("Config or Runner is nil")
	}
	same("Runner().GitDir", c.Runner().GitDir, want.gitDir)

	wts, err := c.Worktrees(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, wt := range wts.Managed {
		paths = append(paths, ui.CanonicalPath(wt.Path))
	}
	var wantPaths []string
	for _, p := range want.managed {
		wantPaths = append(wantPaths, ui.CanonicalPath(p))
	}
	if !equalUnordered(paths, wantPaths) {
		t.Errorf("Worktrees.Managed = %v, want %v", paths, wantPaths)
	}

	mainWT, ok := wts.Main, wts.HasMain
	if ok != (want.main != "") {
		t.Errorf("Worktrees.HasMain = %v, want %v", ok, want.main != "")
	}
	same("Worktrees.Main", mainWT.Path, want.main)
	if ok && mainWT.Branch != "main" {
		t.Errorf("Worktrees.Main branch = %q, want main", mainWT.Branch)
	}
}

func equalUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestOpenOutsideProject(t *testing.T) {
	_, err := Open(context.Background(), t.TempDir(), Options{})
	if !errors.Is(err, config.ErrConfigNotFound) {
		t.Errorf("err = %v, want ErrConfigNotFound", err)
	}
}

func TestResolveDoesNotModifyConfig(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv(WtxHomeEnv, "")
	f := checkoutClone(t, base, "", "")
	cfg, err := config.Load(f.root)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Resolve(context.Background(), f.root, cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorktreeDir != config.DefaultWorktreeDir || cfg.SharedDir != config.DefaultSharedDir {
		t.Errorf("Resolve modified cfg: %q %q", cfg.WorktreeDir, cfg.SharedDir)
	}
	if got := c.Config().WorktreeDir; got != "~/.wtx/myrepo/worktrees" {
		t.Errorf("Config().WorktreeDir = %q", got)
	}
}

func TestOpenDryRunStillQueries(t *testing.T) {
	requireGit(t)
	f := bareClone(t, t.TempDir())
	ctx := context.Background()
	c, err := Open(ctx, f.root, Options{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Runner().DryRun {
		t.Error("Runner().DryRun = false, want true")
	}
	if managed, err := c.ManagedWorktrees(ctx); err != nil || len(managed) != 2 {
		t.Errorf("ManagedWorktrees under dry-run = %v, %v", managed, err)
	}
	if wts, err := c.Worktrees(ctx); err != nil || !wts.HasMain {
		t.Errorf("Worktrees under dry-run: HasMain=%v err=%v", wts.HasMain, err)
	}
}

func TestOpenRejectsInvalidCloneName(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv(WtxHomeEnv, "")
	f := checkoutClone(t, base, "", "../escape")
	if _, err := Open(context.Background(), f.root, Options{}); err == nil {
		t.Error("Open accepted an invalid wtx.name")
	}
}

func TestCloneCheckOwned(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv(WtxHomeEnv, "")
	f := checkoutClone(t, base, "", "")
	ctx := context.Background()
	c, err := Open(ctx, f.root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CheckOwned(); !errors.Is(err, ErrCloneNotSetUp) {
		t.Errorf("no Owner marker: err = %v", err)
	}
	dir, _ := c.CloneDir()

	// Another Project owns the Clone dir.
	other := filepath.Join(base, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, other, "version: 1\ngit_dir: .git\n")
	if err := WriteMarker(dir, other, false); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckOwned(); !errors.Is(err, ErrCloneNotSetUp) || !strings.Contains(err.Error(), "wtx init --name") {
		t.Errorf("other owner: err = %v", err)
	}

	// The owner is no longer a Project: an Orphaned Clone dir.
	if err := os.Remove(filepath.Join(other, config.ConfigFileName)); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckOwned(); !errors.Is(err, ErrCloneNotSetUp) || !strings.Contains(err.Error(), "wtx doctor --fix") {
		t.Errorf("orphaned: err = %v", err)
	}

	if err := WriteMarker(dir, f.root, false); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckOwned(); err != nil {
		t.Errorf("own Owner marker: err = %v", err)
	}

	bare, err := Open(ctx, bareClone(t, filepath.Join(base, "b")).root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := bare.CheckOwned(); err != nil {
		t.Errorf("bare layout: err = %v", err)
	}
}

func TestOpenAtSkipsRootDetection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as git")
	}
	// A git on PATH that logs every call: OpenAt must not run any.
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	writeScript(t, filepath.Join(bin, "git"), "echo \"$@\" >> '"+log+"'\nexit 1\n", 0o755)
	t.Setenv("PATH", bin)

	root := t.TempDir()
	writeConfig(t, root, "version: 1\ngit_dir: .bare\n")
	c, err := OpenAt(context.Background(), root, Options{DryRun: true, Quiet: true, BatchMode: true})
	if err != nil {
		t.Fatal(err)
	}
	if c.Root() != root || c.Layout() != BareLayout {
		t.Errorf("Root, Layout = %q, %v; want %q, bare", c.Root(), c.Layout(), root)
	}
	if r := c.Runner(); !r.DryRun || !r.Quiet || !r.BatchMode {
		t.Errorf("Runner DryRun, Quiet, BatchMode = %v, %v, %v; want all true", r.DryRun, r.Quiet, r.BatchMode)
	}
	if calls, err := os.ReadFile(log); err == nil {
		t.Errorf("OpenAt ran git:\n%s", calls)
	}
	// Open's root detection does call git, so the spy above can see a call.
	if _, err := Open(context.Background(), root, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(log); err != nil {
		t.Errorf("Open ran no git through the spy: %v", err)
	}
}

func TestOpenAtUsesGivenRoot(t *testing.T) {
	requireGit(t)
	base := t.TempDir()
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv(WtxHomeEnv, "")
	f := checkoutClone(t, base, "", "")
	// A nested .worktree.yml that root detection would pass over in favor of
	// the repository's root.
	inner := filepath.Join(f.root, "inner")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, inner, "version: 1\ngit_dir: .bare\n")
	ctx := context.Background()
	if c, err := Open(ctx, inner, Options{}); err != nil || c.Root() != f.root {
		t.Fatalf("Open(inner) root = %v, %v; want %q", c, err, f.root)
	}
	c, err := OpenAt(ctx, inner, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Root() != inner {
		t.Errorf("OpenAt(inner).Root() = %q, want %q", c.Root(), inner)
	}
}

func TestOpenAtWithoutConfig(t *testing.T) {
	_, err := OpenAt(context.Background(), t.TempDir(), Options{})
	if !errors.Is(err, config.ErrConfigNotFound) {
		t.Errorf("err = %v, want ErrConfigNotFound", err)
	}
}

// TestClassifyWorktreesManaged drives the Managed worktree filter without
// git: the bare entry and the Project root (by exact or symlinked path) are
// dropped and every other worktree is kept in order.
func TestClassifyWorktreesManaged(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	feature := filepath.Join(root, "worktrees", "feature")
	external := filepath.Join(base, "elsewhere", "fix")

	tests := []struct {
		name string
		all  []git.WorktreeInfo
		want []string
	}{
		{"empty list", nil, nil},
		{"bare entry", []git.WorktreeInfo{{Path: filepath.Join(root, ".bare"), Bare: true}}, nil},
		{"project root", []git.WorktreeInfo{{Path: root, Branch: "main"}}, nil},
		{"symlinked project root", []git.WorktreeInfo{{Path: link, Branch: "main"}}, nil},
		{"linked worktrees", []git.WorktreeInfo{
			{Path: filepath.Join(root, ".bare"), Bare: true},
			{Path: root, Branch: "main"},
			{Path: feature, Branch: "feature"},
			{Path: external, Branch: "fix"},
		}, []string{feature, external}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, wt := range ClassifyWorktrees(tt.all, root, CheckoutLayout, "main").Managed {
				got = append(got, wt.Path)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("managed = %q, want %q", got, tt.want)
			}
		})
	}
}
