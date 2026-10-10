package project

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
)

// NameConfigKey is the local git config key naming a clone's ~/.wtx/<name>
// directory. It lives in the repository's common config, so every linked
// worktree of the clone sees it, and it is never committed.
const NameConfigKey = "wtx.name"

// NameSource records where a clone's ~/.wtx/<name> came from.
type NameSource string

const (
	// NameFromGitConfig means the name is git config wtx.name.
	NameFromGitConfig NameSource = "git-config"
	// NameFromDirectory means wtx.name is unset and the name defaults to
	// the repository directory name.
	NameFromDirectory NameSource = "directory"
	// NameFromOverride means the name was chosen explicitly (wtx init
	// --name), ahead of wtx.name.
	NameFromOverride NameSource = "override"
)

// CloneName is the ~/.wtx/<name> name of one clone and where it came from.
type CloneName struct {
	Name   string
	Source NameSource
}

// Describe explains the name and its origin for messages.
func (n CloneName) Describe() string {
	switch n.Source {
	case NameFromGitConfig:
		return fmt.Sprintf("name %q from git config %s", n.Name, NameConfigKey)
	case NameFromOverride:
		return fmt.Sprintf("name %q chosen explicitly", n.Name)
	}
	return fmt.Sprintf("name %q is the repository directory name (git config %s is unset)", n.Name, NameConfigKey)
}

// ReadCloneName resolves the Clone name of the Clone at root, reading
// wtx.name through runner: git config wtx.name when set, otherwise the
// directory name of root. A git directory that cannot be read falls back to
// the directory name, so diagnostics still work; an invalid wtx.name is an
// error. Resolve reads it for a Clone with a Clone dir; doctor reads it for
// the Clone dir an In-repo layout would migrate to.
func ReadCloneName(ctx context.Context, runner *git.Runner, root string) (CloneName, error) {
	// LocalConfig reports "" on a read error: the directory name applies.
	current, _, _ := runner.LocalConfig(ctx, NameConfigKey)
	return pickCloneName(filepath.Base(root), nil, current)
}

// pickCloneName applies Clone name precedence: override when non-nil, else
// current (git config wtx.name) when non-empty, else base (the directory
// name of the Project root). The picked name must be valid.
func pickCloneName(base string, override *string, current string) (CloneName, error) {
	n := CloneName{Name: base, Source: NameFromDirectory}
	switch {
	case override != nil:
		n = CloneName{Name: *override, Source: NameFromOverride}
	case current != "":
		n = CloneName{Name: current, Source: NameFromGitConfig}
	}
	if err := ValidateCloneName(n.Name); err != nil {
		if n.Source == NameFromGitConfig {
			return CloneName{}, fmt.Errorf("git config %s: %w", NameConfigKey, err)
		}
		return CloneName{}, err
	}
	return n, nil
}

// ErrNoCloneDir is returned by PlanClone when .worktree.yml spells out every
// path (or the Project has a Bare layout), so the Clone has no Clone dir.
var ErrNoCloneDir = errors.New("clone has no clone directory")

// ClonePlan is the per-clone setup wtx init performs: which Clone dir this
// clone uses, under which Clone name, and whether wtx.name must be written.
type ClonePlan struct {
	Name     CloneName
	CloneDir string
	// Config is the planned .worktree.yml with its omitted paths resolved
	// to CloneDir. Callers must not modify it.
	Config *config.Config
	// Current is wtx.name before setup, or "" when it is unset.
	Current string
}

// SetName reports whether wtx.name must be written.
func (p *ClonePlan) SetName() bool { return p.Current != p.Name.Name }

// PlanClone plans setting up the Clone at root for cfg, which it does not
// modify. override, when non-nil, is the Clone name chosen explicitly; it
// wins over wtx.name and the directory name (see pickCloneName). The Clone
// dir must be free for root: an error wrapping ErrCloneDirTaken means
// another directory name is needed. A cfg without per-clone defaults
// returns ErrNoCloneDir before git is read. Unlike ReadCloneName, a git
// config that cannot be read is an error, since setup is about to write it.
func PlanClone(ctx context.Context, runner *git.Runner, root string, cfg *config.Config, override *string) (*ClonePlan, error) {
	if !cfg.HomeDefaults() {
		return nil, ErrNoCloneDir
	}
	current, _, err := runner.LocalConfig(ctx, NameConfigKey)
	if err != nil {
		return nil, err
	}
	name, err := pickCloneName(filepath.Base(root), override, current)
	if err != nil {
		return nil, err
	}
	dir, err := SelectCloneDir(root, name.Name)
	if err != nil {
		return nil, err
	}
	resolved := *cfg
	ApplyHomeConfigPaths(&resolved, name.Name)
	return &ClonePlan{Name: name, CloneDir: dir, Config: &resolved, Current: current}, nil
}

// ApplyHomeConfigPaths fills the worktree_dir and shared_dir that cfg leaves
// unset with their HomeConfigPaths(name) spellings. Explicit values are kept.
func ApplyHomeConfigPaths(cfg *config.Config, name string) {
	layout := HomeConfigPaths(name)
	if !cfg.WorktreeDirSet() {
		cfg.WorktreeDir = layout.WorktreeDir
	}
	if !cfg.SharedDirSet() {
		cfg.SharedDir = layout.SharedDir
	}
}

// ErrCloneNotSetUp is returned by Clone.CheckOwned when this Clone has not
// run wtx init.
var ErrCloneNotSetUp = errors.New("this clone is not set up for wtx")

// configuredCloneDir returns the Clone dir that a resolved cfg uses for its
// per-clone defaults, or false when it has none.
func configuredCloneDir(root string, cfg *config.Config) (string, bool) {
	if !cfg.HomeDefaults() {
		return "", false
	}
	path := SharedPath(root, cfg)
	if !cfg.WorktreeDirSet() {
		path = WorktreesPath(root, cfg)
	}
	return CloneDirOf(path)
}
