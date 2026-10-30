package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project/fscopy"
	"github.com/bkildow/wtx/internal/ui"
)

// ShareState is what sharing a path did in one worktree.
type ShareState int

const (
	// ShareDone means the worktree got the link or copy (or would, under
	// dry-run).
	ShareDone ShareState = iota
	// ShareAlready means the worktree already links to the shared path.
	ShareAlready
	// ShareSkipped means the worktree has its own file at the path, which
	// was left alone.
	ShareSkipped
)

// ShareOutcome is the result of sharing a path into one worktree.
type ShareOutcome struct {
	Worktree git.WorktreeInfo
	State    ShareState
}

// CheckShareSource checks that rel, relative to sourceDir, can be shared:
// it exists, is not a symlink, and no directory above it (below sourceDir)
// is a symlink. It changes nothing.
func CheckShareSource(sourceDir, rel string) error {
	if hasSymlinkParent(sourceDir, rel) {
		return fmt.Errorf("%s is inside a symlinked directory; share the directory instead", rel)
	}
	info, err := os.Lstat(filepath.Join(sourceDir, rel))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("%s does not exist in %s", rel, sourceDir)
	case err != nil:
		return err
	case info.Mode()&os.ModeSymlink != 0:
		return fmt.Errorf("%s is already a symlink", filepath.Join(sourceDir, rel))
	}
	return nil
}

// checkSharedFree checks that dir/rel does not exist yet, so sharing rel
// into dir (shared/copy or shared/symlink) replaces nothing.
func checkSharedFree(dir, rel string) error {
	if hasSymlinkParent(dir, rel) {
		return fmt.Errorf("%s is inside a symlinked directory of %s", rel, dir)
	}
	path := filepath.Join(dir, rel)
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// LinkBack links symlinkDir/rel to sourceDir/rel, so worktrees that link to
// the shared entry resolve to the source's own file. It reports already when
// the entry exists and already points there; any other entry is an error.
func LinkBack(symlinkDir, sourceDir, rel string, dryRun bool) (already bool, err error) {
	entry, source := filepath.Join(symlinkDir, rel), filepath.Join(sourceDir, rel)
	if pointsAt(entry, source) {
		return true, nil
	}
	if err := checkSharedFree(symlinkDir, rel); err != nil {
		return false, err
	}
	if dryRun {
		ui.DryRunNotice(fmt.Sprintf("symlink %s -> %s", entry, source))
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		return false, err
	}
	return false, os.Symlink(source, entry)
}

// MoveToShared moves sourceDir/rel into symlinkDir/rel and links the source
// to it. If the link cannot be made, the file is moved back.
func MoveToShared(symlinkDir, sourceDir, rel string, dryRun bool) error {
	src, dst := filepath.Join(sourceDir, rel), filepath.Join(symlinkDir, rel)
	if err := checkSharedFree(symlinkDir, rel); err != nil {
		return err
	}
	if dryRun {
		ui.DryRunNotice(fmt.Sprintf("move %s -> %s", src, dst))
		ui.DryRunNotice(fmt.Sprintf("symlink %s -> %s", src, dst))
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := fscopy.Move(src, dst); err != nil {
		return err
	}
	if err := os.Symlink(dst, src); err != nil {
		if undo := fscopy.Move(dst, src); undo != nil {
			return fmt.Errorf("link %s: %w; moving it back failed, it is now at %s: %w", src, err, dst, undo)
		}
		return fmt.Errorf("link %s: %w (moved it back)", src, err)
	}
	return nil
}

// CopyToShared copies sourceDir/rel into copyDir/rel.
func CopyToShared(copyDir, sourceDir, rel string, dryRun bool) error {
	src, dst := filepath.Join(sourceDir, rel), filepath.Join(copyDir, rel)
	if err := checkSharedFree(copyDir, rel); err != nil {
		return err
	}
	if dryRun {
		ui.DryRunNotice(fmt.Sprintf("copy %s -> %s", src, dst))
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := fscopy.CopyAll(src, dst); err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	return nil
}

// LinkInto links rel from symlinkDir into each worktree, as ApplySymlinks
// would for that one path, but never removes anything: a worktree that
// already has something else at the link is skipped.
func LinkInto(symlinkDir, rel string, wts []git.WorktreeInfo, dryRun bool) ([]ShareOutcome, error) {
	outcomes := make([]ShareOutcome, 0, len(wts))
	for _, wt := range wts {
		link, target := PlannedSymlink(symlinkDir, wt.Path, rel)
		out := ShareOutcome{Worktree: wt}
		info, err := os.Lstat(link)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if dryRun {
				ui.DryRunNotice(fmt.Sprintf("symlink %s -> %s", link, target))
			} else if err := os.Symlink(target, link); err != nil {
				return outcomes, err
			}
		case err != nil:
			return outcomes, err
		case info.Mode()&os.ModeSymlink != 0 && pointsAt(link, target):
			out.State = ShareAlready
		default:
			out.State = ShareSkipped
		}
		outcomes = append(outcomes, out)
	}
	return outcomes, nil
}

// pointsAt reports whether the symlink link resolves to the same place as
// target.
func pointsAt(link, target string) bool {
	dest, err := os.Readlink(link)
	return err == nil && SamePath(dest, target)
}

// SeedInto copies sourceDir/rel (a file, or every file below a directory)
// into each worktree that lacks it; files a worktree already has are kept.
// A worktree that gets nothing is reported as skipped.
func SeedInto(sourceDir, rel string, wts []git.WorktreeInfo, dryRun bool) ([]ShareOutcome, error) {
	files, err := filesBelow(sourceDir, rel)
	if err != nil {
		return nil, err
	}
	outcomes := make([]ShareOutcome, 0, len(wts))
	for _, wt := range wts {
		n, err := seedFiles(sourceDir, files, wt.Path, dryRun)
		if err != nil {
			return outcomes, err
		}
		out := ShareOutcome{Worktree: wt}
		if n == 0 {
			out.State = ShareSkipped
		}
		outcomes = append(outcomes, out)
	}
	return outcomes, nil
}

// filesBelow returns rel itself when it is not a directory, else every
// non-directory entry below dir/rel, relative to dir.
func filesBelow(dir, rel string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(filepath.Join(dir, rel), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		r, err := filepath.Rel(dir, path)
		files = append(files, r)
		return err
	})
	return files, err
}

// AppendWorktreeInclude appends pattern as a line of root/.worktreeinclude,
// creating the file when it is missing.
func AppendWorktreeInclude(root, pattern string, dryRun bool) error {
	path := filepath.Join(root, git.WorktreeIncludeFile)
	if dryRun {
		ui.DryRunNotice(fmt.Sprintf("append %s to %s", pattern, path))
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	content := string(data)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content+pattern+"\n"), 0o644)
}
