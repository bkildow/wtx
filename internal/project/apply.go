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
	Kept      int // shared/copy files left alone because the worktree already had them
	Symlinked int
}

// CopyMode says what ApplyCopy does with a shared/copy file the worktree
// already has. The zero value replaces it, as a fresh worktree wants:
// shared/copy wins over the .worktreeinclude files copied before it.
type CopyMode struct {
	// Seed keeps the worktree's existing files, so re-applying never resets
	// a populated directory such as vendor/ to an older shared snapshot.
	Seed bool
	// Replace lists shared/copy paths (cleaned, as ReplacePaths returns
	// them) that Seed still replaces. A directory covers everything
	// below it; a template matches by its own name or its output's.
	Replace []string
	// seeded holds the worktree paths (cleaned) the .worktreeinclude layer
	// copied in this same Apply. They are not the worktree's own, so Seed
	// still replaces them and shared/copy wins as on a new worktree.
	seeded map[string]bool
}

var (
	// ReplaceExisting is the CopyMode for a new worktree: shared/copy
	// replaces what .worktreeinclude copied before it.
	ReplaceExisting = CopyMode{}
	// KeepExisting is the CopyMode for an existing worktree: only files it
	// is missing are copied.
	KeepExisting = CopyMode{Seed: true}
)

// keeps reports whether m leaves the existing destination of rel (the
// shared/copy path) alone; out is the worktree path rel renders to.
func (m CopyMode) keeps(rel, out string) bool {
	if !m.Seed || m.seeded[out] {
		return false
	}
	for _, p := range m.Replace {
		if ui.Within(p, rel) || ui.Within(p, out) {
			return false
		}
	}
	return true
}

// ReplacePaths checks the --overwrite paths a user named against copyDir
// (shared/copy) and returns them cleaned for CopyMode.Replace. Each must be
// relative, stay inside the worktree, and name an entry of copyDir (or the
// output of a template there), so a typo fails before anything is written.
func ReplacePaths(copyDir string, paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		if !filepath.IsLocal(p) {
			return nil, fmt.Errorf("%s: give a path inside the worktree, relative to its root", p)
		}
		clean := filepath.Clean(p)
		if clean == "." {
			return nil, fmt.Errorf("%s: use --force to replace every shared file", p)
		}
		if !inCopyDir(filepath.Join(copyDir, clean)) && !inCopyDir(filepath.Join(copyDir, clean+".template")) {
			return nil, fmt.Errorf("%s is not in %s", p, copyDir)
		}
		out = append(out, clean)
	}
	return out, nil
}

// inCopyDir reports whether path is an entry of shared/copy. Unlike exists,
// any Lstat error counts as missing, so a path below a file (ENOTDIR) is
// refused rather than accepted and then matching nothing.
func inCopyDir(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// IncludeSource is the main worktree and the files its .worktreeinclude
// selects (paths relative to Dir), as listed by git.ListWorktreeIncludes.
type IncludeSource struct {
	Dir   string
	Files []string
}

// applyInclude copies the .worktreeinclude-selected files from the main
// worktree into worktreePath and returns the ones it copied (or would copy
// under dryRun). Files that already exist in the destination are left alone,
// matching Claude Code and Conductor, which only seed at creation. It is a
// no-op when include is nil, empty, or the destination is the main worktree
// itself.
func applyInclude(include *IncludeSource, worktreePath string, dryRun bool) ([]string, error) {
	if include == nil || len(include.Files) == 0 || SamePath(include.Dir, worktreePath) {
		return nil, nil
	}
	ui.Step("Copying .worktreeinclude files from " + include.Dir)
	return seedFiles(include.Dir, include.Files, worktreePath, dryRun)
}

// seedFiles copies files (relative to srcDir) into worktreePath, skipping
// any the worktree already has, and returns the ones it copied (or would
// copy under dryRun).
func seedFiles(srcDir string, files []string, worktreePath string, dryRun bool) ([]string, error) {
	var seeded []string
	for _, rel := range files {
		src := filepath.Join(srcDir, rel)
		dest := filepath.Join(worktreePath, rel)

		// Seed only: never replace a file the worktree already has, so a
		// re-run of wtx apply keeps a worktree's own edits.
		if _, err := os.Lstat(dest); err == nil {
			continue
		}
		// A parent that is a symlink (e.g. a directory linked from
		// shared/symlink) would make the copy land outside the worktree.
		if hasSymlinkParent(worktreePath, rel) {
			continue
		}

		if dryRun {
			ui.DryRunNotice(fmt.Sprintf("copy %s -> %s", src, dest))
			seeded = append(seeded, rel)
			continue
		}

		info, err := os.Lstat(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue // removed since git listed it
			}
			return seeded, err
		}
		// Sockets, FIFOs and devices cannot be copied; skip rather than
		// failing the whole apply after the worktree already exists.
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			ui.Warning("  skipped " + rel + ": not a regular file")
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return seeded, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := fscopy.CopySymlink(src, dest); err != nil {
				return seeded, err
			}
		} else if err := fscopy.CopyFile(src, dest); err != nil {
			return seeded, err
		}
		ui.Info("  copied " + rel)
		seeded = append(seeded, rel)
	}
	return seeded, nil
}

// hasSymlinkParent reports whether any existing directory component of rel
// (below root) is a symlink.
func hasSymlinkParent(root, rel string) bool {
	dir := root
	parts := strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/")
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if err != nil {
			return false
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return true
		}
	}
	return false
}

// ApplyCopy copies shared/copy into worktreePath, rendering .template files
// when vars is set. mode decides what happens to files the worktree already
// has; a file or directory of the worktree's own that is in the way of a
// shared one is always kept. It returns how many files it copied (or would
// copy under dryRun) and how many it kept.
func ApplyCopy(projectRoot, worktreePath string, cfg *config.Config, dryRun bool, vars *TemplateVars, mode CopyMode) (copied, kept int, err error) {
	copyDir := filepath.Join(SharedPath(projectRoot, cfg), "copy")

	if _, err := os.Stat(copyDir); os.IsNotExist(err) {
		return 0, 0, nil
	}

	ui.Step("Copying shared files")

	logged := make(map[string]bool)

	// Fast path: reflink whole template-free subtrees in one syscall instead of walking file-by-file.
	skipTrees, copied, err := fastPathCopyTrees(copyDir, worktreePath, vars, dryRun, logged)
	if err != nil {
		return 0, 0, err
	}

	// Kept files below a top-level directory are logged as one line once the
	// walk leaves that directory; WalkDir visits each one's files together.
	var keptDir string
	var keptInDir int
	flushKept := func() {
		if keptInDir > 0 {
			ui.Info(fmt.Sprintf("  kept %s/ (%d already in the worktree)", keptDir, keptInDir))
		}
		keptInDir = 0
	}

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
		if topLevel != keptDir {
			flushKept()
			keptDir = topLevel
		}
		if d.IsDir() {
			if skipTrees[topLevel] {
				return filepath.SkipDir
			}
			// A worktree file (or dangling symlink) where shared/copy has a
			// directory is the worktree's own: keep it in every mode rather
			// than fail the worktree half-applied.
			wtPath := filepath.Join(worktreePath, rel)
			if _, err := os.Lstat(wtPath); err == nil && !IsDirOrLinkToDir(wtPath) {
				ui.Warning(fmt.Sprintf("  kept %s: the worktree has a file there", rel))
				kept++
				return filepath.SkipDir
			}
			return nil
		}
		isTemplate := vars != nil && IsTemplateFile(rel)
		out := rel
		if isTemplate {
			out = StripTemplateExt(rel)
		}
		dest := filepath.Join(worktreePath, out)

		info, statErr := os.Lstat(dest)
		// Likewise a worktree directory where shared/copy has a file.
		if statErr == nil && info.IsDir() {
			ui.Warning(fmt.Sprintf("  kept %s: the worktree has a directory there", out))
			kept++
			return nil
		}
		if statErr == nil && mode.keeps(rel, out) {
			if isNested {
				keptInDir++
			} else {
				ui.Info(fmt.Sprintf("  kept %s (already in the worktree)", out))
			}
			kept++
			return nil
		}

		if dryRun {
			if isNested {
				logCopyDir(topLevel, logged, true)
			} else {
				ui.DryRunNotice(fmt.Sprintf("copy %s -> %s", path, dest))
			}
			copied++
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}

		if isTemplate {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			srcInfo, err := os.Stat(path)
			if err != nil {
				return err
			}
			processed := ProcessTemplate(string(content), *vars)
			ui.Info(fmt.Sprintf("  substituted template variables in %s", out))
			copied++
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
		copied++
		return nil
	})
	flushKept()
	return copied, kept, err
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

		// CopyTree requires a fresh dst; if one already exists, the per-file
		// walk decides file by file what to copy.
		if _, lstatErr := os.Lstat(subDst); lstatErr == nil {
			continue
		}

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

		// If source is a directory (or links to one, as wtx share makes in a
		// checkout) and destination is a real directory (not a symlink),
		// symlink individual files inside instead of replacing the whole
		// directory. This preserves worktree-local files like those in
		// .claude/ while still symlinking shared config files.
		if IsDirOrLinkToDir(target) && isRealDir(link) {
			n, err := symlinkDirContents(target, link, worktreePath, dryRun)
			count += n
			if err != nil {
				return count, err
			}
			continue
		}

		if dryRun {
			ui.DryRunNotice(fmt.Sprintf("symlink %s -> %s", link, target))
			continue
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

// IsDirOrLinkToDir reports whether path is a directory or a symlink to one.
func IsDirOrLinkToDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isRealDir reports whether path is a directory and not a symlink to one.
func isRealDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// PlannedSymlink returns the link ApplySymlinks makes in worktreePath for
// rel, a file below symlinkDir that does not exist yet: the first entry along
// rel that the worktree has no real directory for, or rel itself.
func PlannedSymlink(symlinkDir, worktreePath, rel string) (link, target string) {
	sub := ""
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		sub = filepath.Join(sub, part)
		link = filepath.Join(worktreePath, sub)
		if !isRealDir(link) {
			break
		}
	}
	return link, filepath.Join(symlinkDir, sub)
}

// symlinkDirContents symlinks individual entries from srcDir into destDir,
// recursing into subdirectories that already exist at the destination.
// Under dryRun it only prints the links it would make.
func symlinkDirContents(srcDir, destDir, worktreePath string, dryRun bool) (int, error) {
	if !dryRun {
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			return 0, err
		}
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return 0, err
	}

	var count int
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry.Name())
		dest := filepath.Join(destDir, entry.Name())

		// Recurse if the source is a directory and the destination a real one.
		if IsDirOrLinkToDir(src) && isRealDir(dest) {
			n, err := symlinkDirContents(src, dest, worktreePath, dryRun)
			count += n
			if err != nil {
				return count, err
			}
			continue
		}

		if dryRun {
			ui.DryRunNotice(fmt.Sprintf("symlink %s -> %s", dest, src))
			continue
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
// the main worktree, then shared/copy, then shared/symlink. With
// ReplaceExisting later layers win, so machine-local shared files override
// what the main worktree provides; KeepExisting leaves files the worktree
// already has alone.
// include may be nil to skip the .worktreeinclude layer; mode is how
// shared/copy treats files the worktree already has.
func Apply(projectRoot, worktreePath string, cfg *config.Config, dryRun bool, vars *TemplateVars, include *IncludeSource, mode CopyMode) (ApplyResult, error) {
	included, err := applyInclude(include, worktreePath, dryRun)
	if err != nil {
		return ApplyResult{}, err
	}
	if mode.Seed && len(included) > 0 {
		mode.seeded = make(map[string]bool, len(included))
		for _, rel := range included {
			mode.seeded[filepath.Clean(filepath.FromSlash(rel))] = true
		}
	}
	copied, kept, err := ApplyCopy(projectRoot, worktreePath, cfg, dryRun, vars, mode)
	if err != nil {
		return ApplyResult{}, err
	}
	symlinked, err := ApplySymlinks(projectRoot, worktreePath, cfg, dryRun)
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{Included: len(included), Copied: copied, Kept: kept, Symlinked: symlinked}, nil
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
