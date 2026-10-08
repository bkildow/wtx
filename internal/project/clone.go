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

// ReadCloneName resolves the Clone name of the Clone at root, reading
// wtx.name through runner: git config wtx.name when set, otherwise the
// directory name of root. A git directory that cannot be read falls back to
// the directory name, so diagnostics still work; an invalid wtx.name is an
// error. Resolve reads it for a Clone with a Clone dir; doctor reads it for
// the Clone dir an In-repo layout would migrate to.
func ReadCloneName(ctx context.Context, runner *git.Runner, root string) (CloneName, error) {
	if value, ok, err := runner.LocalConfig(ctx, NameConfigKey); err == nil && ok {
		if err := ValidateCloneName(value); err != nil {
			return CloneName{}, fmt.Errorf("git config %s: %w", NameConfigKey, err)
		}
		return CloneName{Name: value, Source: NameFromGitConfig}, nil
	}
	name := filepath.Base(root)
	if err := ValidateCloneName(name); err != nil {
		return CloneName{}, err
	}
	return CloneName{Name: name, Source: NameFromDirectory}, nil
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
