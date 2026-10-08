// Package project provides project-level operations including root detection and scaffold creation.
package project

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/ui"
)

// FindRoot locates the wtx project root for startDir.
//
// It first asks git for the repository's common dir: its parent is the
// project root for both the bare (.bare/) and normal (.git/) layouts, and
// this works from linked worktrees located anywhere on disk. If that
// candidate holds a .worktree.yml it wins, which also keeps a committed
// .worktree.yml inside a worktree from being mistaken for the root.
// Next, a startDir inside ~/.wtx/<name>/ (outside any git worktree) maps to
// the root recorded in that directory's marker (see RootFromMarker).
// Otherwise FindRoot walks up from startDir looking for .worktree.yml.
func FindRoot(startDir string) (string, error) {
	if root, ok := rootFromGitCommonDir(startDir); ok {
		return root, nil
	}
	if root, ok := RootFromMarker(startDir); ok {
		return root, nil
	}
	return walkUpForConfig(startDir)
}

func rootFromGitCommonDir(startDir string) (string, bool) {
	commonDir, err := git.CommonDir(context.Background(), startDir)
	if err != nil {
		return "", false
	}
	candidate := filepath.Dir(commonDir)
	if !config.Exists(candidate) {
		return "", false
	}
	return preferLexicalAncestor(startDir, candidate), true
}

// preferLexicalAncestor returns the ancestor of startDir (as the caller
// spelled it) that resolves to the same directory as root, so the result
// stays consistent with the caller's paths (e.g. /var vs /private/var on
// macOS). It resolves startDir once and strips as many components as it sits
// below root. If root is not an ancestor of startDir, or a symlink between
// them changes the depth, root is returned as is.
func preferLexicalAncestor(startDir, root string) string {
	want := ui.CanonicalPath(root)
	dir := filepath.Clean(startDir)
	rel, ok := ui.RelWithin(want, ui.CanonicalPath(dir))
	if !ok {
		return root
	}
	if rel != "." {
		for range strings.Split(rel, string(filepath.Separator)) {
			dir = filepath.Dir(dir)
		}
	}
	if ui.CanonicalPath(dir) != want {
		return root
	}
	return dir
}

func walkUpForConfig(startDir string) (string, error) {
	dir := startDir
	for {
		if config.Exists(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", config.ErrConfigNotFound
		}
		dir = parent
	}
}

// ScaffoldDirs lists the directories CreateScaffold creates.
func ScaffoldDirs(projectRoot string, cfg *config.Config) []string {
	shared := SharedPath(projectRoot, cfg)
	return []string{
		filepath.Join(shared, "copy"),
		filepath.Join(shared, "symlink"),
		WorktreesPath(projectRoot, cfg),
		BinPath(projectRoot, cfg),
	}
}

func CreateScaffold(projectRoot string, cfg *config.Config, dryRun bool) error {
	for _, dir := range ScaffoldDirs(projectRoot, cfg) {
		if dryRun {
			ui.DryRunNotice("mkdir -p " + dir)
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	return nil
}

var sshURLPattern = regexp.MustCompile(`[^/]+[:/]([^/]+/[^/]+?)(?:\.git)?$`)

func RepoNameFromURL(url string) string {
	// Handle SSH URLs: git@github.com:org/repo.git
	if matches := sshURLPattern.FindStringSubmatch(url); len(matches) > 1 {
		parts := strings.Split(matches[1], "/")
		return parts[len(parts)-1]
	}

	// Handle HTTPS URLs: https://github.com/org/repo.git
	base := filepath.Base(url)
	return strings.TrimSuffix(base, ".git")
}

func GitDirPath(projectRoot string, cfg *config.Config) string {
	return filepath.Join(projectRoot, cfg.GitDir)
}

// WorktreesPath is the absolute directory holding the project's worktrees.
// See ExpandPath for how worktree_dir is resolved.
func WorktreesPath(projectRoot string, cfg *config.Config) string {
	return ExpandOrJoin(projectRoot, cfg.WorktreeDir)
}

// SharedPath is the absolute shared directory (copy/ and symlink/). See
// ExpandPath for how shared_dir is resolved.
func SharedPath(projectRoot string, cfg *config.Config) string {
	return ExpandOrJoin(projectRoot, cfg.SharedDir)
}

// BinPath is the directory for project-level scripts run via `wtx run`. It
// is a sibling of the shared directory: bin/ for cloned projects,
// ~/.wtx/<name>/bin/ for initialized ones and .worktrees/bin/ for in-repo
// ones.
func BinPath(projectRoot string, cfg *config.Config) string {
	return BinFor(SharedPath(projectRoot, cfg))
}
