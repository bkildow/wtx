package project

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/project/fscopy"
	"github.com/bkildow/wtx/internal/ui"
)

// ApplyResult holds counts of files processed during Apply.
type ApplyResult struct {
	Included  int // files copied from the main worktree via .worktreeinclude
	Copied    int
	Symlinked int
}

// IncludeSource is the main worktree and the files its .worktreeinclude
// selects (paths relative to Dir), as listed by git.ListWorktreeIncludes.
type IncludeSource struct {
	Dir   string
	Files []string
}

// ApplyInclude copies the .worktreeinclude-selected files from the main
// worktree into worktreePath. Files that already exist in the destination are
// left alone (not counted), matching Claude Code and Conductor, which only
// seed at creation. It is a no-op when include is nil, empty, or the destination is the main
// worktree itself.
func ApplyInclude(include *IncludeSource, worktreePath string, dryRun bool) (int, error) {
	if include == nil || len(include.Files) == 0 || samePath(include.Dir, worktreePath) {
		return 0, nil
	}

	ui.Step("Copying .worktreeinclude files from " + include.Dir)

	var count int
	for _, rel := range include.Files {
		src := filepath.Join(include.Dir, rel)
		dest := filepath.Join(worktreePath, rel)

		// Seed only: never replace a file the worktree already has, so a
		// re-run of wtx apply keeps a worktree's own edits.
		if _, err := os.Lstat(dest); err == nil {
			continue
		}

		if dryRun {
			ui.DryRunNotice(fmt.Sprintf("copy %s -> %s", src, dest))
			count++
			continue
		}

		info, err := os.Lstat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue // removed since git listed it
			}
			return count, err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return count, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := copySymlink(src, dest); err != nil {
				return count, err
			}
		} else if err := fscopy.CopyFile(src, dest); err != nil {
			return count, err
		}
		ui.Info("  copied " + rel)
		count++
	}
	return count, nil
}

// copySymlink recreates the symlink at src as dest, preserving its target.
func copySymlink(src, dest string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}
	return os.Symlink(target, dest)
}

func samePath(a, b string) bool {
	return resolvePathOrClean(a) == resolvePathOrClean(b)
}

func resolvePathOrClean(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

func ApplyCopy(projectRoot, worktreePath string, cfg *config.Config, dryRun bool, vars *TemplateVars) (int, error) {
	copyDir := filepath.Join(SharedPath(projectRoot, cfg), "copy")

	if _, err := os.Stat(copyDir); os.IsNotExist(err) {
		return 0, nil
	}

	ui.Step("Copying shared files")

	var count int
	logged := make(map[string]bool)

	// Fast path: reflink whole template-free subtrees in one syscall instead of walking file-by-file.
	skipTrees, treeCount, err := fastPathCopyTrees(copyDir, worktreePath, vars, dryRun, logged)
	if err != nil {
		return 0, err
	}
	count += treeCount

	sep := string(filepath.Separator)
	err = filepath.WalkDir(copyDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == copyDir {
			return nil
		}
		rel, err := filepath.Rel(copyDir, path)
		if err != nil {
			return err
		}
		topLevel, _, isNested := strings.Cut(rel, sep)
		if d.IsDir() {
			if skipTrees[topLevel] {
				return filepath.SkipDir
			}
			return nil
		}
		dest := filepath.Join(worktreePath, rel)

		if dryRun {
			if isNested {
				logCopyDir(topLevel, logged, true)
			} else {
				dryDest := dest
				if vars != nil && IsTemplateFile(rel) {
					dryDest = filepath.Join(worktreePath, StripTemplateExt(rel))
				}
				ui.DryRunNotice(fmt.Sprintf("copy %s -> %s", path, dryDest))
			}
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}

		if vars != nil && IsTemplateFile(rel) {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			srcInfo, err := os.Stat(path)
			if err != nil {
				return err
			}
			dest = filepath.Join(worktreePath, StripTemplateExt(rel))
			processed := ProcessTemplate(string(content), *vars)
			ui.Info(fmt.Sprintf("  substituted template variables in %s", StripTemplateExt(rel)))
			count++
			return os.WriteFile(dest, []byte(processed), srcInfo.Mode())
		}

		if err := fscopy.CopyFile(path, dest); err != nil {
			return err
		}
		if isNested {
			logCopyDir(topLevel, logged, false)
		} else {
			ui.Info(fmt.Sprintf("  copied %s", rel))
		}
		count++
		return nil
	})
	return count, err
}

// fastPathCopyTrees reflinks each template-free top-level subtree and returns the
// names the caller's per-file walk should skip, plus the file count for reporting.
func fastPathCopyTrees(copyDir, worktreePath string, vars *TemplateVars, dryRun bool, logged map[string]bool) (skip map[string]bool, totalFiles int, err error) {
	skip = make(map[string]bool)

	entries, err := os.ReadDir(copyDir)
	if err != nil {
		return nil, 0, err
	}

	wantTemplateScan := vars != nil

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		subSrc := filepath.Join(copyDir, entry.Name())
		subDst := filepath.Join(worktreePath, entry.Name())

		hasTemplate, fileCount, scanErr := scanTreeForTemplates(subSrc, wantTemplateScan)
		if scanErr != nil {
			return skip, totalFiles, scanErr
		}
		if hasTemplate {
			continue
		}

		if dryRun {
			logCopyDir(entry.Name(), logged, true)
			skip[entry.Name()] = true
			totalFiles += fileCount
			continue
		}

		// CopyTree requires a fresh dst; if one already exists, let the per-file walk overwrite it.
		if _, lstatErr := os.Lstat(subDst); lstatErr == nil {
			continue
		}

		if cloneErr := fscopy.CopyTree(subSrc, subDst); cloneErr != nil {
			if fscopy.IsReflinkUnsupported(cloneErr) {
				continue
			}
			return skip, totalFiles, cloneErr
		}
		logCopyDir(entry.Name(), logged, false)
		skip[entry.Name()] = true
		totalFiles += fileCount
	}

	return skip, totalFiles, nil
}

func scanTreeForTemplates(dir string, wantTemplateScan bool) (hasTemplate bool, fileCount int, err error) {
	err = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if wantTemplateScan && IsTemplateFile(d.Name()) {
			hasTemplate = true
			return filepath.SkipAll
		}
		fileCount++
		return nil
	})
	if err != nil {
		return false, 0, err
	}
	if hasTemplate {
		return true, 0, nil
	}
	return false, fileCount, nil
}

// ApplySymlinks creates symlinks in the worktree for each top-level entry
// in shared/symlink/. When a destination already exists as a real directory
// (e.g. .claude/ created by Claude Code), the contents are symlinked
// individually instead of replacing the directory.
func ApplySymlinks(projectRoot, worktreePath string, cfg *config.Config, dryRun bool) (int, error) {
	symlinkDir := filepath.Join(SharedPath(projectRoot, cfg), "symlink")

	if _, err := os.Stat(symlinkDir); os.IsNotExist(err) {
		return 0, nil
	}

	ui.Step("Creating symlinks")

	entries, err := os.ReadDir(symlinkDir)
	if err != nil {
		return 0, err
	}

	var count int
	for _, entry := range entries {
		target := filepath.Join(symlinkDir, entry.Name())
		link := filepath.Join(worktreePath, entry.Name())

		if dryRun {
			ui.DryRunNotice(fmt.Sprintf("symlink %s -> %s", link, target))
			continue
		}

		// If source is a directory and destination is a real directory (not a
		// symlink), symlink individual files inside instead of replacing the
		// whole directory. This preserves worktree-local files like those in
		// .claude/ while still symlinking shared config files.
		if entry.IsDir() {
			if info, err := os.Lstat(link); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				n, err := symlinkDirContents(target, link, worktreePath)
				count += n
				if err != nil {
					return count, err
				}
				continue
			}
		}

		// Remove existing file/symlink at destination (error ignored; Symlink will fail if needed)
		_ = os.Remove(link)

		if err := os.Symlink(target, link); err != nil {
			return count, err
		}

		relTarget, _ := filepath.Rel(worktreePath, target)
		ui.Info(fmt.Sprintf("  symlinked %s → %s", entry.Name(), relTarget))
		count++
	}

	return count, nil
}

// symlinkDirContents symlinks individual entries from srcDir into destDir,
// recursing into subdirectories that already exist at the destination.
func symlinkDirContents(srcDir, destDir, worktreePath string) (int, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return 0, err
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return 0, err
	}

	var count int
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry.Name())
		dest := filepath.Join(destDir, entry.Name())

		// Recurse if both source and destination are real directories.
		if entry.IsDir() {
			if info, err := os.Lstat(dest); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				n, err := symlinkDirContents(src, dest, worktreePath)
				count += n
				if err != nil {
					return count, err
				}
				continue
			}
		}

		_ = os.Remove(dest)

		if err := os.Symlink(src, dest); err != nil {
			return count, err
		}

		relName, _ := filepath.Rel(worktreePath, dest)
		relTarget, _ := filepath.Rel(worktreePath, src)
		ui.Info(fmt.Sprintf("  symlinked %s → %s", relName, relTarget))
		count++
	}

	return count, nil
}

// Apply layers files into a worktree in order: .worktreeinclude files from
// the main worktree, then shared/copy, then shared/symlink. Later layers win,
// so machine-local shared files override what the main worktree provides.
// include may be nil to skip the .worktreeinclude layer.
func Apply(projectRoot, worktreePath string, cfg *config.Config, dryRun bool, vars *TemplateVars, include *IncludeSource) (ApplyResult, error) {
	included, err := ApplyInclude(include, worktreePath, dryRun)
	if err != nil {
		return ApplyResult{}, err
	}
	copied, err := ApplyCopy(projectRoot, worktreePath, cfg, dryRun, vars)
	if err != nil {
		return ApplyResult{}, err
	}
	symlinked, err := ApplySymlinks(projectRoot, worktreePath, cfg, dryRun)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Included: included, Copied: copied, Symlinked: symlinked}, nil
}

// logCopyDir logs a top-level directory copy once, collapsing nested files.
func logCopyDir(name string, logged map[string]bool, dryRun bool) {
	if logged[name] {
		return
	}
	if dryRun {
		ui.DryRunNotice(fmt.Sprintf("copy %s/", name))
	} else {
		ui.Info(fmt.Sprintf("  copied %s/", name))
	}
	logged[name] = true
}
