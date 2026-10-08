package cmd

import (
	"context"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
)

// resolveIncludeSource locates the main worktree and lists the files its
// .worktreeinclude selects. It returns nil when there is no main worktree,
// no .worktreeinclude, or nothing matches. Failures are reported as warnings
// because a missing include layer should not block creating a worktree.
// worktrees is the repository's worktree list when the caller already has
// it; nil lists them when needed.
func resolveIncludeSource(ctx context.Context, projectRoot string, cfg *config.Config, worktrees []git.WorktreeInfo) *project.IncludeSource {
	mainPath, err := mainWorktreeSource(ctx, projectRoot, cfg, worktrees)
	if err != nil {
		ui.Warning("Could not locate main worktree for .worktreeinclude: " + err.Error())
		return nil
	}
	if mainPath == "" {
		return nil
	}

	files, err := git.ListWorktreeIncludes(ctx, mainPath)
	if err != nil {
		ui.Warning("Could not read .worktreeinclude: " + err.Error())
		return nil
	}
	files = dropManagedPaths(files, mainPath, []string{
		project.WorktreesPath(projectRoot, cfg),
		project.SharedPath(projectRoot, cfg),
		project.BinPath(projectRoot, cfg),
	})
	if len(files) == 0 {
		return nil
	}
	return &project.IncludeSource{Dir: mainPath, Files: files}
}

// mainWorktreeSource returns the worktree .worktreeinclude is read from: the
// project root for wtx init projects (whose git dir is the checkout's .git),
// or the worktree checked out on the main branch for clone/bare projects.
func mainWorktreeSource(ctx context.Context, projectRoot string, cfg *config.Config, worktrees []git.WorktreeInfo) (string, error) {
	if cfg.IsCheckoutLayout() {
		return projectRoot, nil
	}

	if worktrees == nil {
		// Read-only lookup: a non-dry runner so --dry-run still finds the source.
		runner := git.NewRunner(project.GitDirPath(projectRoot, cfg), false)
		runner.BatchMode = true
		var err error
		if worktrees, err = runner.WorktreeList(ctx); err != nil {
			return "", err
		}
	}
	return resolveMainWorktreePath(worktrees, filterManagedWorktrees(worktrees, projectRoot), cfg.MainBranchOrDefault()), nil
}

// dropManagedPaths removes files (relative to srcDir) that live inside wtx's
// own directories, which can sit under the main worktree in init projects
// (e.g. .worktrees/shared/copy/.env would otherwise match a ".env" pattern).
func dropManagedPaths(files []string, srcDir string, managedDirs []string) []string {
	var prefixes []string
	for _, dir := range managedDirs {
		// A managed directory that is srcDir itself would drop everything.
		if rel, ok := ui.RelWithin(srcDir, dir); ok && rel != "." {
			prefixes = append(prefixes, filepath.ToSlash(rel)+"/")
		}
	}
	if len(prefixes) == 0 {
		return files
	}

	var kept []string
	for _, f := range files {
		if !slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(f, p) }) {
			kept = append(kept, f)
		}
	}
	return kept
}
