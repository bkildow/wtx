package doctor

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
)

// Per-worktree filesystem and Git reads dominate doctor's runtime on projects
// with many worktrees and large shared trees. They are gathered concurrently
// into worktreeFacts, then consumed in worktree order so the report stays
// deterministic and inspection state is only touched from one goroutine.

// collected is a goroutine-local finding list merged into the report later.
type collected []Finding

func (c *collected) add(id string, severity Severity, path, explanation, remedy string) {
	*c = append(*c, Finding{ID: id, Severity: severity, Path: path, Explanation: explanation, Remedy: remedy})
}

func (c *collected) problem(id, path string, err error) {
	c.add(id, "fail", path, "Inspection failed: "+err.Error(), "Correct the file or access permissions and rerun wtx doctor.")
}

type trackedFile struct {
	rel, blob string
}

type worktreeFacts struct {
	managed      bool      // a Managed worktree, which gets shared files
	copies       collected // shared.copy findings
	danglingErrs collected // shared.symlink walk failures
	dangling     []string  // links into the shared symlink tree whose targets are gone
	tracked      []trackedFile
	modified     map[string]bool // tracked paths whose content may differ from the index blob
	trackedErrs  collected
}

// sharedCopy is one shared/copy destination relative to a worktree root.
// Dependency directories (vendor/, node_modules/, ...) are seeds that setup
// tools rewrite after the copy, so only their presence is checked.
type sharedCopy struct {
	rel string
	dir bool
}

type sharedCopies struct {
	entries []sharedCopy
}

func (s *inspection) listSharedCopies() sharedCopies {
	copyDir := filepath.Join(s.clone.SharedDir(), "copy")
	var out sharedCopies
	err := filepath.WalkDir(copyDir, func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) && path == copyDir {
			return nil
		}
		if err != nil {
			s.problem("shared.copy", path, err)
			return nil
		}
		if repairArtifact(entry.Name()) {
			return nil
		}
		dependency := entry.IsDir() && path != copyDir && skippedName(entry.Name())
		if entry.IsDir() && !dependency {
			return nil
		}
		rel, err := filepath.Rel(copyDir, path)
		if err != nil {
			return err
		}
		if dependency {
			out.entries = append(out.entries, sharedCopy{rel: rel, dir: true})
			return filepath.SkipDir
		}
		out.entries = append(out.entries, sharedCopy{rel: project.StripTemplateExt(rel)})
		return nil
	})
	if err != nil {
		s.problem("shared.copy", copyDir, err)
	}
	return out
}

// prefetch gathers facts for every worktree in all concurrently, keyed by
// path. Only the managed worktrees get shared-file facts.
func (s *inspection) prefetch(ctx context.Context, all, managed []git.WorktreeInfo) map[string]*worktreeFacts {
	copies := s.listSharedCopies()
	symlinkRoot := ui.CanonicalPath(filepath.Join(s.clone.SharedDir(), "symlink"))
	linkDirs := symlinkDirs(symlinkRoot)
	facts := map[string]*worktreeFacts{}
	var paths []string
	for _, wt := range all {
		if wt.Bare || wt.Prunable || facts[wt.Path] != nil {
			continue
		}
		if info, err := os.Stat(wt.Path); err != nil || !info.IsDir() {
			continue
		}
		facts[wt.Path] = &worktreeFacts{}
		paths = append(paths, wt.Path)
	}
	for _, wt := range managed {
		if f := facts[wt.Path]; f != nil {
			f.managed = true
		}
	}
	var wg sync.WaitGroup
	// Directory reads contend in the kernel; more workers than this made
	// doctor slower, not faster, in measurements on macOS.
	sem := make(chan struct{}, 4)
	for _, path := range paths {
		f := facts[path]
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if f.managed {
				f.copies = checkSharedCopies(path, copies)
				f.dangling, f.danglingErrs = findDanglingLinks(path, symlinkRoot, linkDirs)
			}
			f.tracked, f.modified, f.trackedErrs = listTracked(ctx, path)
		})
	}
	wg.Wait()
	return facts
}

// checkSharedCopies reads each destination directory once instead of
// stat-ing every file, and reports a wholly missing directory once.
func checkSharedCopies(root string, copies sharedCopies) collected {
	var out collected
	type listing struct {
		entries map[string]fs.DirEntry
		missing bool
	}
	dirs := map[string]*listing{}
	var list func(rel string) *listing
	list = func(rel string) *listing {
		if l, ok := dirs[rel]; ok {
			return l
		}
		l := &listing{}
		dirs[rel] = l
		if rel != "." && list(filepath.Dir(rel)).missing {
			l.missing = true
			return l
		}
		dir := filepath.Join(root, rel)
		entries, err := os.ReadDir(dir)
		switch {
		case os.IsNotExist(err):
			l.missing = true
		case err != nil:
			out.problem("shared.copy", dir, err)
		default:
			l.entries = make(map[string]fs.DirEntry, len(entries))
			for _, e := range entries {
				l.entries[e.Name()] = e
			}
		}
		return l
	}
	reported := map[string]bool{}
	for _, c := range copies.entries {
		rel := c.rel
		dir := filepath.Dir(rel)
		l := list(dir)
		if l.missing {
			top := dir
			for p := filepath.Dir(top); p != "." && list(p).missing; p = filepath.Dir(p) {
				top = p
			}
			if !reported[top] {
				reported[top] = true
				out.add("shared.copy", "warn", filepath.Join(root, top), "Expected shared copy directory is missing.", "Review and run wtx apply for this worktree (or wtx apply --all).")
			}
			continue
		}
		if l.entries == nil {
			continue // ReadDir failure already reported
		}
		dest := filepath.Join(root, rel)
		entry, ok := l.entries[filepath.Base(rel)]
		if !ok {
			if c.dir {
				out.add("shared.copy", "warn", dest, "Expected shared copy directory is missing.", "Review and run wtx apply for this worktree (or wtx apply --all).")
			} else {
				out.add("shared.copy", "warn", dest, "Expected shared copy is missing.", "Review and run wtx apply for this worktree (or wtx apply --all).")
			}
			continue
		}
		isDir := entry.IsDir()
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := os.Stat(dest)
			if os.IsNotExist(err) {
				out.add("shared.copy", "warn", dest, "Expected shared copy is missing.", "Review and run wtx apply for this worktree (or wtx apply --all).")
				continue
			}
			if err != nil {
				out.problem("shared.copy", dest, err)
				continue
			}
			isDir = info.IsDir()
		}
		switch {
		case c.dir && !isDir:
			out.add("shared.copy", "fail", dest, "Expected shared directory is a file.", "Review the destination before running wtx apply.")
		case !c.dir && isDir:
			out.add("shared.copy", "fail", dest, "Expected shared file is a directory.", "Review the destination before running wtx apply.")
		}
	}
	return out
}

// symlinkDirs lists the directories of the shared symlink tree relative to
// its root ("." included). wtx apply only places managed links inside the
// mirrors of these directories.
func symlinkDirs(sharedRoot string) []string {
	var dirs []string
	_ = filepath.WalkDir(sharedRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return nil
		}
		if path != sharedRoot && repairArtifact(entry.Name()) {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(sharedRoot, path)
		if err == nil {
			dirs = append(dirs, rel)
		}
		return nil
	})
	return dirs
}

// findDanglingLinks finds links into the shared symlink tree whose targets no
// longer exist. A deleted shared source is no longer enumerable, so the
// destination directories mirroring the shared tree are listed directly;
// walking the whole worktree is prohibitively slow on large projects.
func findDanglingLinks(root, sharedRoot string, dirs []string) ([]string, collected) {
	var links []string
	var errs collected
	for _, rel := range dirs {
		dir := filepath.Join(root, rel)
		if rel != "." {
			// Skip mirrors that are missing or are themselves links.
			if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
				continue
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				errs.problem("shared.symlink", dir, err)
			}
			continue
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			target, err := os.Readlink(path)
			if err != nil {
				errs.problem("shared.symlink", path, err)
				continue
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(dir, target)
			}
			if !ui.Within(sharedRoot, ui.CanonicalPath(target)) {
				continue
			}
			if _, err := os.Stat(path); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				errs.problem("shared.symlink", path, err)
				continue
			}
			links = append(links, path)
		}
	}
	return links, errs
}

// listTracked returns tracked files with their index blobs, plus the paths
// whose worktree content may differ from that blob.
func listTracked(ctx context.Context, root string) ([]trackedFile, map[string]bool, collected) {
	var errs collected
	path, err := git.WorktreeConfigPath(root)
	if err != nil {
		errs.problem("migration.scan", root, err)
		return nil, nil, errs
	}
	runner := git.NewRunner(filepath.Dir(path), false)
	runner.Quiet = true
	// Explicit work-tree and bare override allow scanning before compatibility repair.
	base := []string{"--work-tree", root, "-c", "core.bare=false"}
	output, err := runner.QueryRaw(ctx, append(base, "ls-files", "-s", "-z")...)
	if err != nil {
		errs.problem("migration.scan", root, err)
		return nil, nil, errs
	}
	var files []trackedFile
	for _, record := range strings.Split(output, "\x00") {
		// <mode> SP <blob> SP <stage> TAB <path>
		meta, rel, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !ok || rel == "" || len(fields) != 3 || slices.ContainsFunc(strings.Split(rel, "/"), skippedName) {
			continue
		}
		files = append(files, trackedFile{rel: rel, blob: fields[1]})
	}
	modified := map[string]bool{}
	output, err = runner.QueryRaw(ctx, append(base, "diff-files", "--name-only", "-z")...)
	if err != nil {
		// Without the dirty list, treat every file as possibly modified.
		for _, f := range files {
			modified[f.rel] = true
		}
		return files, modified, errs
	}
	for _, rel := range strings.Split(output, "\x00") {
		if rel != "" {
			modified[rel] = true
		}
	}
	return files, modified, errs
}
