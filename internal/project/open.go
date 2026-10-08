package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/ui"
)

// LayoutKind is a Clone's Layout: how its git data, worktrees and shared
// files are arranged on disk.
type LayoutKind int

const (
	// BareLayout keeps the git data in a bare repository and everything else
	// under the Project root (wtx clone).
	BareLayout LayoutKind = iota + 1
	// CheckoutLayout is built on an ordinary checkout. Its machine-local state
	// lives in the Clone dir, or wherever explicit worktree_dir and
	// shared_dir point outside the Project root.
	CheckoutLayout
	// InRepoLayout is a Checkout layout whose worktrees directory lies inside
	// the Project root (wtx init --in-repo).
	InRepoLayout
)

func (k LayoutKind) String() string {
	switch k {
	case BareLayout:
		return "bare"
	case CheckoutLayout:
		return "checkout"
	case InRepoLayout:
		return "in-repo"
	default:
		return fmt.Sprintf("LayoutKind(%d)", int(k))
	}
}

// Options controls how a Clone is opened.
type Options struct {
	// DryRun makes the Clone's Runner print commands that change the
	// repository instead of running them. Queries still run.
	DryRun bool
	// Quiet keeps the Clone's Runner from echoing the git commands it runs,
	// for structured reports such as wtx doctor.
	Quiet bool
	// BatchMode keeps the Clone's Runner from prompting for credentials,
	// for non-interactive callers such as the Claude Code hooks.
	BatchMode bool
}

// Clone is one machine's copy of a Project, resolved once: its Layout,
// Clone name and every directory wtx uses. It is immutable; after rewriting
// .worktree.yml, Resolve again.
type Clone struct {
	root      string
	cfg       *config.Config
	layout    LayoutKind
	name      CloneName
	cloneDir  string
	worktrees string
	shared    string
	runner    *git.Runner
}

// Open finds the Project root for startDir (see FindRoot), loads its
// .worktree.yml and resolves the Clone (see Resolve). Outside a Project it
// returns config.ErrConfigNotFound. It prints nothing.
func Open(ctx context.Context, startDir string, opts Options) (*Clone, error) {
	root, err := FindRoot(startDir)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	return Resolve(ctx, root, cfg, opts)
}

// Resolve resolves the Clone at root from an already loaded cfg, which it
// does not modify: it picks the Layout, reads the Clone name for a Clone
// dir (git config wtx.name, else the directory name of root), fills the
// per-clone defaults and checks that every configured path expands.
func Resolve(ctx context.Context, root string, cfg *config.Config, opts Options) (*Clone, error) {
	resolved := *cfg
	gitDir := GitDirPath(root, &resolved)
	c := &Clone{root: root, cfg: &resolved, runner: git.NewRunner(gitDir, opts.DryRun)}
	c.runner.Quiet = opts.Quiet
	c.runner.BatchMode = opts.BatchMode

	if resolved.HomeDefaults() {
		// Reading the name is never part of a command's output, and it
		// runs under dry-run.
		quiet := git.NewRunner(gitDir, false)
		quiet.Quiet = true
		name, err := ReadCloneName(ctx, quiet, root)
		if err != nil {
			return nil, err
		}
		c.name = name
		ApplyHomeConfigPaths(&resolved, name.Name)
	}
	if err := validatePaths(root, &resolved); err != nil {
		return nil, err
	}

	c.worktrees = WorktreesPath(root, &resolved)
	c.shared = SharedPath(root, &resolved)
	c.cloneDir, _ = configuredCloneDir(root, &resolved)

	switch {
	case !resolved.IsCheckoutLayout():
		c.layout = BareLayout
	case insideRoot(root, c.worktrees):
		c.layout = InRepoLayout
	default:
		c.layout = CheckoutLayout
	}
	return c, nil
}

// insideRoot reports whether path lies strictly inside root.
func insideRoot(root, path string) bool {
	rel, ok := ui.RelWithin(ui.CanonicalPath(root), ui.CanonicalPath(path))
	return ok && rel != "."
}

// Root is the Project root: the directory holding .worktree.yml.
func (c *Clone) Root() string { return c.root }

// Layout is the Clone's Layout.
func (c *Clone) Layout() LayoutKind { return c.layout }

// Name is the Clone name that picks the Clone dir, and where it came from.
// It is the zero CloneName when the Clone has no Clone dir.
func (c *Clone) Name() CloneName { return c.name }

// CloneDir is the Clone's directory in the wtx home (~/.wtx/<name>), or
// false when .worktree.yml spells out both worktree_dir and shared_dir (or
// the Clone has a Bare layout).
func (c *Clone) CloneDir() (string, bool) { return c.cloneDir, c.cloneDir != "" }

// GitDir is the absolute git directory: the bare repository or the
// checkout's .git.
func (c *Clone) GitDir() string { return c.runner.GitDir }

// WorktreesDir is the absolute directory new worktrees are created in.
func (c *Clone) WorktreesDir() string { return c.worktrees }

// SharedDir is the absolute shared files directory (copy/ and symlink/).
func (c *Clone) SharedDir() string { return c.shared }

// BinDir is the absolute directory of scripts run by `wtx run`, the sibling
// bin/ of SharedDir.
func (c *Clone) BinDir() string { return BinFor(c.shared) }

// Config is the resolved .worktree.yml: per-clone defaults are filled in.
// Callers must not modify it.
func (c *Clone) Config() *config.Config { return c.cfg }

// Runner runs git against GitDir, honoring Options.DryRun.
func (c *Clone) Runner() *git.Runner { return c.runner }

// ManagedWorktrees lists the Clone's Managed worktrees: every linked
// worktree, wherever it lives on disk. Bare entries and, in a Checkout
// layout, the Project root (the Main worktree) are left out. It runs under
// dry-run.
func (c *Clone) ManagedWorktrees(ctx context.Context) ([]git.WorktreeInfo, error) {
	all, err := c.runner.WorktreeList(ctx)
	if err != nil {
		return nil, err
	}
	return c.managed(all), nil
}

func (c *Clone) managed(all []git.WorktreeInfo) []git.WorktreeInfo {
	root := ui.CanonicalPath(c.root)
	var managed []git.WorktreeInfo
	for _, wt := range all {
		if wt.Bare || wt.Path == c.root || ui.CanonicalPath(wt.Path) == root {
			continue
		}
		managed = append(managed, wt)
	}
	return managed
}

// MainWorktree returns the Main worktree: in a Checkout or In-repo layout
// the Project root itself, in a Bare layout the Managed worktree checked out
// on the main branch (config.Config.MainBranchOrDefault), or false when
// there is none. It runs under dry-run.
func (c *Clone) MainWorktree(ctx context.Context) (git.WorktreeInfo, bool, error) {
	all, err := c.runner.WorktreeList(ctx)
	if err != nil {
		return git.WorktreeInfo{}, false, err
	}
	if c.layout != BareLayout {
		root := ui.CanonicalPath(c.root)
		for _, wt := range all {
			if !wt.Bare && ui.CanonicalPath(wt.Path) == root {
				wt.Path = c.root
				return wt, true, nil
			}
		}
		return git.WorktreeInfo{Path: c.root}, true, nil
	}
	branch := c.cfg.MainBranchOrDefault()
	for _, wt := range c.managed(all) {
		if wt.Branch == branch {
			return wt, true, nil
		}
	}
	return git.WorktreeInfo{}, false, nil
}

// CloneDirs returns the distinct Clone dirs that WorktreesDir and SharedDir
// live in, whether they are the per-clone default or spelled out in
// .worktree.yml.
func (c *Clone) CloneDirs() []string {
	var dirs []string
	for _, path := range []string{c.worktrees, c.shared} {
		if dir, ok := CloneDirOf(path); ok && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// CheckOwned checks, before anything is created there, that the Clone dir
// belongs to this Clone (its Owner marker names the Project root). Clones
// without a Clone dir always pass.
func (c *Clone) CheckOwned() error {
	dir, ok := c.CloneDir()
	if !ok {
		return nil
	}
	m, err := ReadMarker(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%w: %s has no %s\n  run 'wtx init' in %s to set it up", ErrCloneNotSetUp, dir, MarkerFileName, c.root)
	case err != nil:
		return fmt.Errorf("%w: %w\n  run 'wtx doctor' for details", ErrCloneNotSetUp, err)
	case SamePath(m.Root, c.root):
		return nil
	case config.Exists(m.Root):
		return fmt.Errorf("%w: %s belongs to %s\n  give this clone its own directory with 'wtx init --name <other>'", ErrCloneNotSetUp, dir, m.Root)
	default:
		return fmt.Errorf("%w: %s belongs to %s, which is no longer a wtx project\n  run 'wtx doctor --fix' to record this clone as its owner, or 'wtx init --name <other>'", ErrCloneNotSetUp, dir, m.Root)
	}
}
