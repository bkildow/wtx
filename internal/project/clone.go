package project

import (
	"context"
	"errors"
	"fmt"
	"os"
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
)

// CloneName is the ~/.wtx/<name> name of one clone and where it came from.
type CloneName struct {
	Name   string
	Source NameSource
}

// Describe explains the name and its origin for messages.
func (n CloneName) Describe() string {
	if n.Source == NameFromGitConfig {
		return fmt.Sprintf("name %q from git config %s", n.Name, NameConfigKey)
	}
	return fmt.Sprintf("name %q is the repository directory name (git config %s is unset)", n.Name, NameConfigKey)
}

// ReadCloneName resolves the ~/.wtx/<name> name of the clone at root: git
// config wtx.name when set, otherwise the directory name of root. A git
// directory that cannot be read falls back to the directory name, so
// diagnostics still work; an invalid wtx.name is an error.
func ReadCloneName(ctx context.Context, root string, cfg *config.Config) (CloneName, error) {
	runner := git.NewRunner(GitDirPath(root, cfg), false)
	runner.Quiet = true
	if value, ok, err := runner.LocalConfig(ctx, NameConfigKey); err == nil && ok {
		if err := ValidateProjectName(value); err != nil {
			return CloneName{}, fmt.Errorf("git config %s: %w", NameConfigKey, err)
		}
		return CloneName{Name: value, Source: NameFromGitConfig}, nil
	}
	name := filepath.Base(root)
	if err := ValidateProjectName(name); err != nil {
		return CloneName{}, err
	}
	return CloneName{Name: name, Source: NameFromDirectory}, nil
}

// ApplyHomeLayout fills the worktree_dir and shared_dir that cfg leaves
// unset with their HomeLayout(name) spellings. Explicit values are kept.
func ApplyHomeLayout(cfg *config.Config, name string) {
	layout := HomeLayout(name)
	if !cfg.WorktreeDirSet {
		cfg.WorktreeDir = layout.WorktreeDir
	}
	if !cfg.SharedDirSet {
		cfg.SharedDir = layout.SharedDir
	}
}

// ResolveLayout resolves the per-clone defaults of a checkout-layout
// project (see config.Config.HomeDefaults): omitted worktree_dir and
// shared_dir become ~/.wtx/<name>/worktrees and ~/.wtx/<name>/shared for
// the clone's name (see ReadCloneName). Other projects are left unchanged
// and report ok == false.
func ResolveLayout(ctx context.Context, root string, cfg *config.Config) (name CloneName, ok bool, err error) {
	if !cfg.HomeDefaults() {
		return CloneName{}, false, nil
	}
	name, err = ReadCloneName(ctx, root, cfg)
	if err != nil {
		return CloneName{}, false, err
	}
	ApplyHomeLayout(cfg, name.Name)
	return name, true, nil
}

// LoadConfig loads the config of the project at root, resolves per-clone
// defaults (ResolveLayout) and checks that its paths expand.
func LoadConfig(ctx context.Context, root string) (*config.Config, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	if _, _, err := ResolveLayout(ctx, root, cfg); err != nil {
		return nil, err
	}
	if err := ValidatePaths(root, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ErrCloneNotSetUp is returned by CheckCloneSetup when this clone has not
// run wtx init.
var ErrCloneNotSetUp = errors.New("this clone is not set up for wtx")

// CloneHomeDir returns the ~/.wtx/<name> directory that a resolved cfg
// uses for its per-clone defaults, or false when it has none.
func CloneHomeDir(root string, cfg *config.Config) (string, bool) {
	if !cfg.HomeDefaults() {
		return "", false
	}
	path := SharedPath(root, cfg)
	if !cfg.WorktreeDirSet {
		path = WorktreesPath(root, cfg)
	}
	return HomeProjectDirOf(path)
}

// CheckCloneSetup checks, before anything is created there, that the
// per-clone ~/.wtx/<name> directory of a resolved cfg belongs to the clone
// at root. Projects without per-clone defaults always pass.
func CheckCloneSetup(root string, cfg *config.Config) error {
	dir, ok := CloneHomeDir(root, cfg)
	if !ok {
		return nil
	}
	m, err := ReadMarker(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%w: %s has no %s\n  run 'wtx init' in %s to set it up", ErrCloneNotSetUp, dir, MarkerFileName, root)
	case err != nil:
		return fmt.Errorf("%w: %w\n  run 'wtx doctor' for details", ErrCloneNotSetUp, err)
	case SamePath(m.Root, root):
		return nil
	case config.Exists(m.Root):
		return fmt.Errorf("%w: %s belongs to %s\n  give this clone its own directory with 'wtx init --name <other>'", ErrCloneNotSetUp, dir, m.Root)
	default:
		return fmt.Errorf("%w: %s belongs to %s, which is no longer a wtx project\n  run 'wtx doctor --fix' to record this clone as its owner, or 'wtx init --name <other>'", ErrCloneNotSetUp, dir, m.Root)
	}
}
