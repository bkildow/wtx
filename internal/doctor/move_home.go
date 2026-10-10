package doctor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/project/fscopy"
	"github.com/bkildow/wtx/internal/ui"
)

// migrateID is the finding and repair ID of the opt-in Migration of an
// In-repo layout (.worktrees/) to a Clone dir (~/.wtx/<name>/).
const migrateID = "home.migrate"

// migration is the plan for one --migrate-home run. Source directories are
// empty when there is nothing left to move, which makes a re-run after a
// partial failure pick up where the previous run stopped.
type migration struct {
	s        *inspection
	cloneDir string // ~/.wtx/<name>, expanded
	oldWT    string // in-repo worktree directory, or ""
	oldShare string // in-repo shared directory, or ""
	newWT    string
	newShare string
	name     string // wtx.name to record, or "" when the config already resolves to cloneDir
	steps    []repair
}

// oldBin is the in-repo bin directory (next to oldShare) when it lies under
// oldWT, or "". Elsewhere, such as the repository-root bin/ next to
// shared_dir: shared (the layout wtx init wrote before --in-repo), it may
// belong to the project and never moves.
func (m *migration) oldBin() string {
	if m.oldShare == "" || m.oldWT == "" {
		return ""
	}
	if bin := ui.CanonicalPath(project.BinFor(m.oldShare)); ui.Within(m.oldWT, bin) {
		return bin
	}
	return ""
}

// planMigration plans the Migration of an In-repo layout to a Clone dir.
// Every step is an operation repair that re-checks its own preconditions, so
// the plan is safe to apply after a partial failure and is a no-op once done.
//
// Steps are registered by step rather than inspection.plan: they target
// directories and operations, not snapshotted files under managed parents.
func (s *inspection) planMigration(ctx context.Context, managed []git.WorktreeInfo, name string) {
	cfgPath := filepath.Join(s.clone.Root(), config.ConfigFileName)
	if s.clone.Layout() == project.BareLayout {
		s.add(migrateID, Warn, cfgPath, "--migrate-home applies only to projects set up with wtx init (git_dir: .git); this project keeps its layout.", "")
		return
	}
	m := &migration{s: s}
	wtDir := ui.CanonicalPath(s.clone.WorktreesDir())
	shareDir := ui.CanonicalPath(s.clone.SharedDir())
	moveWT, moveShare := s.clone.Layout() == project.InRepoLayout, project.InsideRoot(s.clone.Root(), shareDir)
	legacy := filepath.Join(s.clone.Root(), project.InRepoConfigPaths().WorktreeDir)

	// Sources: the configured directories while they are in the repository,
	// otherwise what an interrupted migration left in .worktrees/.
	if moveWT {
		m.oldWT = wtDir
	} else if isDir(legacy) {
		m.oldWT = legacy
	}
	if moveShare {
		m.oldShare = shareDir
	} else if legacyShare := filepath.Join(s.clone.Root(), project.InRepoConfigPaths().SharedDir); m.oldWT == legacy && isDir(legacyShare) {
		// The config moved on without this clone's in-repo files (e.g. a
		// teammate pulled a migrated .worktree.yml): move them along with
		// the worktrees instead of leaving them behind.
		m.oldShare = legacyShare
	}
	oldBin := m.oldBin()
	if m.oldShare != "" && m.oldWT == "" {
		s.add(migrateID, Fail, cfgPath, "shared_dir is inside the repository but worktree_dir is not.", "Move the shared and bin directories manually and update shared_dir and scripts in "+config.ConfigFileName+".")
		return
	}
	if m.oldShare != "" && ui.Within(m.oldShare, m.oldWT) {
		// Moving shared_dir would carry the worktrees along without git
		// worktree move.
		s.add(migrateID, Fail, cfgPath, "worktree_dir is inside shared_dir.", "Move the worktrees and shared directory manually and update worktree_dir and shared_dir in "+config.ConfigFileName+".")
		return
	}

	// Destination: the ~/.wtx/<name> directory the config already names
	// (re-run), else --name or the repository directory name.
	var err error
	if dirs := s.clone.CloneDirs(); len(dirs) > 0 {
		m.cloneDir = dirs[0]
		err = project.CheckCloneDir(m.cloneDir, s.clone.Root())
		if moveWT || moveShare {
			// The moved directory's key is dropped from the config and then
			// resolves per clone through wtx.name, which must name cloneDir.
			m.name = filepath.Base(m.cloneDir)
		}
	} else {
		if name == "" {
			// wtx.name (e.g. from an interrupted run), else the directory name.
			clone, cerr := project.ReadCloneName(ctx, s.clone.Runner(), s.clone.Root())
			if cerr != nil {
				s.add(migrateID, Fail, cfgPath, cerr.Error()+".", "Choose a name with wtx doctor --migrate-home --name <name>.")
				return
			}
			name = clone.Name
		}
		var dir string
		if dir, err = project.SelectCloneDir(s.clone.Root(), name); dir == "" {
			s.add(migrateID, Fail, cfgPath, err.Error()+".", "Choose a name with wtx doctor --migrate-home --name <name>.")
			return
		}
		m.cloneDir = ui.CanonicalPath(dir)
		m.name = name
	}
	if err != nil {
		s.add(migrateID, Fail, m.cloneDir, err.Error()+".", "Choose another directory name with wtx doctor --migrate-home --name <name>.")
		return
	}
	m.newWT, m.newShare = wtDir, shareDir
	home := project.ClonePaths(m.cloneDir)
	if moveWT {
		m.newWT = home.WorktreeDir
	}
	if moveShare {
		m.newShare = home.SharedDir
	}
	s.addManaged(m.cloneDir)

	if m.oldWT == "" && m.oldShare == "" {
		s.add(migrateID, OK, m.cloneDir, "Worktrees and shared files already live outside the repository.", "")
		return
	}
	if cwd, err := os.Getwd(); err == nil {
		for _, dir := range []string{m.oldWT, m.oldShare} {
			if dir != "" && ui.Within(dir, ui.CanonicalPath(cwd)) {
				s.add(migrateID, Fail, cwd, "The current directory is inside "+dir+", which the migration moves.", "Run wtx doctor --migrate-home from the Project root: "+s.clone.Root())
				return
			}
		}
	}
	from := m.oldWT
	if from == "" {
		from = filepath.Dir(m.oldShare)
	}
	finding := s.add(migrateID, Warn, m.cloneDir, "Worktrees, shared files and scripts move from "+s.paths.Path(from)+" to "+ui.DisplayPath("", m.cloneDir)+".", "Planned steps are listed under Repairs; --dry-run previews them without changing anything.")

	m.planMarker()
	m.planName(ctx)
	keepShare := m.planDir(ctx, "shared", m.oldShare, m.newShare)
	// bin/ is resolved as a sibling of shared_dir, so it stays wherever shared/ stays.
	keepBin := keepShare
	if keepShare && oldBin != "" && exists(oldBin) {
		s.addSubject(migrateID, oldBin, "bin", "The bin directory stays next to the shared directory, which stays in the repository.", "Untrack the shared files and rerun wtx doctor --migrate-home to move both.")
	} else if !keepShare {
		keepBin = m.planDir(ctx, "bin", oldBin, project.BinFor(m.newShare))
	}
	if projectBin := project.BinFor(m.oldShare); m.oldShare != "" && oldBin == "" && !keepShare && !noFiles(projectBin) {
		s.addSubject(migrateID, ui.CanonicalPath(projectBin), "bin", "The bin directory is outside worktree_dir and may belong to the project; it stays in the repository.", "Scripts that wtx run found there by name need a scripts entry in "+config.ConfigFileName+", or move them to "+ui.DisplayPath("", project.BinFor(m.newShare))+".")
	}
	moved := m.planWorktrees(managed)
	if m.oldShare != "" && !keepShare {
		m.planRelink()
	}
	if len(moved) > 0 {
		m.planRepair(moved)
	}
	// Only a directory kept under worktree_dir blocks removing it.
	keptInside := (keepShare && ui.Within(m.oldWT, m.oldShare)) || (oldBin != "" && keepBin)
	if m.oldWT != "" && !keptInside {
		m.planCleanup()
	}
	moveShare = moveShare && !keepShare
	if len(m.steps) > 0 || moveWT || moveShare {
		m.planConfig(moveWT, moveShare, oldBin != "" && !keepBin)
	}
	if len(m.steps) > 0 {
		s.report.Findings[finding].Repairable = true
	}
}

func (m *migration) step(target, action string, run func(context.Context) error, done func() error) {
	r := repair{id: migrateID, file: snapshot{path: target}, action: action, run: run, done: done, backups: m.s.backups}
	r.guards = append([]snapshot(nil), m.s.guards...)
	m.s.repairs = append(m.s.repairs, r)
	m.steps = append(m.steps, r)
}

func (m *migration) planMarker() {
	if mk, err := project.ReadMarker(m.cloneDir); err == nil && project.SamePath(mk.Root, m.s.clone.Root()) {
		return
	}
	root, cloneDir := m.s.clone.Root(), m.cloneDir
	m.step(filepath.Join(cloneDir, project.MarkerFileName), "write owner marker",
		func(context.Context) error {
			if err := project.CheckCloneDir(cloneDir, root); err != nil {
				return err
			}
			return project.WriteMarker(cloneDir, root, false)
		},
		func() error {
			mk, err := project.ReadMarker(cloneDir)
			if err == nil && !project.SamePath(mk.Root, root) {
				err = fmt.Errorf("marker names %s", mk.Root)
			}
			return err
		})
}

// planName records the clone's name in local git config (wtx.name) unless
// it already holds it, so the per-clone defaults the rewritten config relies
// on resolve to cloneDir.
func (m *migration) planName(ctx context.Context) {
	if m.name == "" {
		return
	}
	runner, name := m.s.clone.Runner(), m.name
	if current, ok, err := runner.LocalConfig(ctx, project.NameConfigKey); err == nil && ok && current == name {
		return
	}
	m.step(filepath.Join(m.s.gitDir, "config"), "set git config "+project.NameConfigKey+" "+name,
		func(ctx context.Context) error { return runner.SetLocalConfig(ctx, project.NameConfigKey, name) },
		func() error {
			current, _, err := runner.LocalConfig(context.Background(), project.NameConfigKey)
			if err == nil && current != name {
				err = fmt.Errorf("git config %s is %q, not %q", project.NameConfigKey, current, name)
			}
			return err
		})
}

// planDir plans moving a shared or bin directory. It reports true when the
// directory must stay where it is (tracked by Git or a conflict).
func (m *migration) planDir(ctx context.Context, label, from, to string) (keep bool) {
	if from == "" || !exists(from) {
		return false
	}
	rel, _ := filepath.Rel(m.s.clone.Root(), from)
	if out, err := m.s.clone.Runner().QueryRaw(ctx, "-C", m.s.clone.Root(), "ls-files", "-z", "--", rel); err != nil {
		m.s.problem(migrateID, from, err)
		return true
	} else if out != "" {
		m.s.addSubject(migrateID, from, label, "The "+label+" directory contains files tracked by Git; it stays in the repository.", "Keep it, or untrack it and rerun wtx doctor --migrate-home to move it.")
		return true
	}
	if !noFiles(to) {
		m.s.addSubject(migrateID, to, label, "Destination "+label+" directory already exists and is not empty.", "Merge "+from+" into it manually, then rerun wtx doctor --migrate-home.")
		return true
	}
	m.step(to, "move "+m.s.paths.Path(from)+" → "+ui.DisplayPath("", to),
		func(context.Context) error { return moveDir(from, to) },
		func() error { return movedCheck(from, to) })
	return false
}

// planWorktrees plans moving the Managed worktrees under oldWT and returns
// their destinations.
func (m *migration) planWorktrees(managed []git.WorktreeInfo) (moved []string) {
	if m.oldWT == "" {
		return nil
	}
	oldBin := m.oldBin()
	for _, wt := range managed {
		src := ui.CanonicalPath(wt.Path)
		if !ui.Within(m.oldWT, src) ||
			(m.oldShare != "" && ui.Within(m.oldShare, src)) || (oldBin != "" && ui.Within(oldBin, src)) {
			continue
		}
		rel, err := filepath.Rel(m.oldWT, src)
		if err != nil {
			m.s.problem(migrateID, wt.Path, err)
			continue
		}
		dest := filepath.Join(m.newWT, rel)
		switch {
		case wt.Locked:
			m.s.addSubject(migrateID, wt.Path, wt.Branch, "Worktree is locked; it was not moved.", unlockRemedy(wt.Path))
			continue
		case wt.Prunable || !isDir(src):
			m.s.addSubject(migrateID, wt.Path, wt.Branch, "Worktree directory is missing or prunable; it was not moved.", "Review git worktree prune --dry-run, then rerun wtx doctor --migrate-home.")
			continue
		case hasSubmodules(src):
			m.s.addSubject(migrateID, wt.Path, wt.Branch, "Worktree has submodules, which git worktree move cannot move; it was not moved.", "Remove it with wtx remove and recreate it with wtx add, or move it manually.")
			continue
		case setupRunning(src):
			m.s.addSubject(migrateID, wt.Path, wt.Branch, "Setup is still running in this worktree; it was not moved.", "Wait for setup to finish, then rerun wtx doctor --migrate-home.")
			continue
		case exists(dest):
			m.s.addSubject(migrateID, dest, wt.Branch, "Destination already exists; the worktree was not moved.", "Move or remove "+dest+", then rerun wtx doctor --migrate-home.")
			continue
		}
		moved = append(moved, dest)
		runner := m.s.clone.Runner()
		m.step(dest, "git worktree move "+m.s.paths.Path(src)+" → "+ui.DisplayPath("", dest),
			func(ctx context.Context) error {
				if !isDir(src) {
					return fmt.Errorf("worktree is no longer at %s", src)
				}
				if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
					return err
				}
				return runner.WorktreeMove(ctx, src, dest)
			},
			func() error { return movedCheck(src, filepath.Join(dest, ".git")) })
	}
	return moved
}

// planRelink retargets managed symlinks in every Managed worktree from the
// old shared/symlink tree to the new one.
func (m *migration) planRelink() {
	oldLinks, newLinks := filepath.Join(m.oldShare, "symlink"), filepath.Join(m.newShare, "symlink")
	clone := m.s.clone
	relinkAll := func(ctx context.Context, checkOnly bool) error {
		if !isDir(newLinks) {
			return nil
		}
		managed, err := clone.ManagedWorktrees(ctx)
		if err != nil {
			return err
		}
		var errs []error
		for _, wt := range managed {
			if isDir(wt.Path) {
				errs = append(errs, relink(newLinks, oldLinks, wt.Path, checkOnly))
			}
		}
		return errors.Join(errs...)
	}
	// done re-checks the links so a failed retarget keeps the config
	// unchanged and a rerun plans this step again.
	m.step(newLinks, "retarget shared symlinks in worktrees",
		func(ctx context.Context) error { return relinkAll(ctx, false) },
		func() error { return relinkAll(context.Background(), true) })
}

func (m *migration) planRepair(moved []string) {
	runner := m.s.clone.Runner()
	m.step(m.newWT, "git worktree repair", func(ctx context.Context) error {
		var present []string
		for _, p := range moved {
			if isDir(p) {
				present = append(present, p)
			}
		}
		if len(present) == 0 {
			return nil
		}
		return runner.WorktreeRepair(ctx, present...)
	}, nil)
}

func (m *migration) planCleanup() {
	dir := m.oldWT
	m.step(dir, "remove "+m.s.paths.Path(dir)+" if empty", func(context.Context) error {
		return removeEmptyDirs(dir)
	}, nil)
}

// planConfig removes worktree_dir and shared_dir for the directories that
// moved, so they resolve per clone to ~/.wtx/<name>/, and the scripts
// entries that pointed into the old bin directory: wtx run finds a script
// in the bin directory by file name. An entry whose name differs from its
// file name is rewritten to the ~/.wtx/<name>/bin path instead. It runs last
// (every step checks the config is unchanged) and only once every earlier
// step is done.
func (m *migration) planConfig(wt, shared, bin bool) {
	path := filepath.Join(m.s.clone.Root(), config.ConfigFileName)
	file, err := takeSnapshot(path)
	if err != nil {
		m.s.problem(migrateID, path, err)
		return
	}
	layout := project.HomeConfigPaths(filepath.Base(m.cloneDir))
	want := *m.s.clone.Config()
	want.Scripts = maps.Clone(m.s.clone.Config().Scripts)
	if wt {
		want.SetWorktreeDir("")
	}
	if shared {
		want.SetSharedDir("")
	}
	if bin {
		oldBin := m.oldBin()
		for name, value := range want.Scripts {
			p := ui.CanonicalPath(project.ExpandOrJoin(m.s.clone.Root(), value))
			rel, ok := ui.RelWithin(oldBin, p)
			switch {
			case !ok:
			case rel == name:
				delete(want.Scripts, name)
			default:
				want.Scripts[name] = layout.Bin + "/" + filepath.ToSlash(rel)
			}
		}
	}
	data, err := rewriteConfig(file.data, &want)
	if err != nil {
		m.s.add(migrateID, Fail, path, "Cannot rewrite "+config.ConfigFileName+" in place: "+err.Error()+".", "Remove worktree_dir and shared_dir (and the scripts entries under the old bin directory) manually.")
		return
	}
	if bytes.Equal(data, file.data) {
		return
	}
	steps := slices.Clone(m.steps)
	r := repair{id: migrateID, file: file, data: data, action: "remove worktree_dir, shared_dir and bin scripts entries (per-clone ~/.wtx/<name> defaults)", backups: m.s.backups}
	r.guards = append([]snapshot(nil), m.s.guards...)
	r.validate = func() error {
		for _, step := range steps {
			if step.done == nil {
				continue
			}
			if err := step.done(); err != nil {
				return fmt.Errorf("an earlier migration step did not complete (%w); config left unchanged", err)
			}
		}
		return nil
	}
	m.s.repairs = append(m.s.repairs, r)
	m.steps = append(m.steps, r)
}

var (
	topLevelKey = regexp.MustCompile(`^([A-Za-z_]+):(.*)$`)
	scriptEntry = regexp.MustCompile(`^(\s+)("[^"]*"|'[^']*'|[^\s:#][^:#]*?):\s*(.*)$`)
)

// rewriteConfig edits .worktree.yml line by line, preserving comments and
// layout: it removes worktree_dir and shared_dir when want leaves them unset,
// and removes or rewrites block-style scripts entries to match want.Scripts
// (dropping a scripts key left without entries). It then checks that the
// result parses to want.
func rewriteConfig(data []byte, want *config.Config) ([]byte, error) {
	lines := strings.Split(string(data), "\n")
	var out []string
	scriptsHeader, scriptEntries := -1, 0
	inScripts := false
	for _, line := range lines {
		if k := topLevelKey.FindStringSubmatch(line); k != nil {
			inScripts = k[1] == "scripts"
			switch {
			case k[1] == "worktree_dir" && !want.WorktreeDirSet():
				continue
			case k[1] == "shared_dir" && !want.SharedDirSet():
				continue
			case inScripts && strings.TrimSpace(strings.SplitN(k[2], "#", 2)[0]) == "":
				scriptsHeader = len(out)
			}
			out = append(out, line)
			continue
		}
		e := scriptEntry.FindStringSubmatch(line)
		if !inScripts || e == nil {
			out = append(out, line)
			continue
		}
		var name string
		if err := yaml.Unmarshal([]byte(e[2]), &name); err != nil {
			out = append(out, line)
			scriptEntries++
			continue
		}
		v, ok := want.Scripts[name]
		if !ok {
			continue
		}
		var have string
		_ = yaml.Unmarshal([]byte(e[3]), &have)
		if have != v {
			line = e[1] + e[2] + ": " + config.YAMLQuote(v)
		}
		out = append(out, line)
		scriptEntries++
	}
	if scriptsHeader >= 0 && scriptEntries == 0 {
		out = slices.Delete(out, scriptsHeader, scriptsHeader+1)
	}
	result := []byte(strings.Join(out, "\n"))
	got, err := config.Parse(result)
	if err != nil {
		return nil, err
	}
	if got.WorktreeDirSet() != want.WorktreeDirSet() || got.SharedDirSet() != want.SharedDirSet() ||
		(want.WorktreeDirSet() && got.WorktreeDir != want.WorktreeDir) || (want.SharedDirSet() && got.SharedDir != want.SharedDir) ||
		!maps.Equal(got.Scripts, want.Scripts) {
		return nil, errors.New("unrecognized layout (flow-style or multi-line values)")
	}
	return result, nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func emptyOrMissing(dir string) bool {
	entries, err := os.ReadDir(dir)
	return errors.Is(err, os.ErrNotExist) || (err == nil && len(entries) == 0)
}

// noFiles reports whether dir is missing or holds only (nested) empty
// directories, such as the shared/copy and shared/symlink scaffold that wtx
// init creates.
func noFiles(dir string) bool {
	found := false
	err := filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return err == nil && !found
}

// movedCheck verifies a move: the source is gone and the destination exists.
func movedCheck(from, to string) error {
	if exists(from) {
		return fmt.Errorf("%s still exists", from)
	}
	if !exists(to) {
		return fmt.Errorf("%s does not exist", to)
	}
	return nil
}

// moveDir renames from to to, falling back to copy-then-remove across
// filesystems. An empty destination directory is replaced.
func moveDir(from, to string) error {
	if !exists(from) {
		if exists(to) {
			return nil // already moved
		}
		return fmt.Errorf("%s does not exist", from)
	}
	if !noFiles(to) {
		return fmt.Errorf("%s already exists and is not empty", to)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if exists(to) {
		// Only empty directories (e.g. the wtx init scaffold): replace them.
		if err := os.RemoveAll(to); err != nil {
			return err
		}
	}
	err := os.Rename(from, to)
	if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyTree(from, to); err != nil {
		// Drop the partial copy so a rerun does not see a non-empty destination.
		_ = os.RemoveAll(to)
		return fmt.Errorf("copy %s to %s: %w (the source is unchanged)", from, to, err)
	}
	return os.RemoveAll(from)
}

func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(to, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(dest, info.Mode().Perm())
		case entry.Type()&os.ModeSymlink != 0:
			return fscopy.CopySymlink(path, dest)
		default:
			return fscopy.CopyFile(path, dest)
		}
	})
}

// relink retargets symlinks under worktree that point at oldRoot/<rel> to
// newRoot/<rel>, walking only the mirror of newRoot. Other links are left
// alone. With checkOnly it changes nothing and reports a link that still
// needs retargeting as an error.
func relink(newRoot, oldRoot, worktree string, checkOnly bool) error {
	oldRoot = ui.CanonicalPath(oldRoot)
	return filepath.WalkDir(newRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == newRoot {
			return nil
		}
		rel, err := filepath.Rel(newRoot, path)
		if err != nil {
			return err
		}
		link := filepath.Join(worktree, rel)
		info, err := os.Lstat(link)
		if err != nil {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if entry.IsDir() && info.IsDir() {
				return nil // mirrored real directory: descend
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target, err := os.Readlink(link)
		if err == nil && ui.CanonicalPath(target) == filepath.Join(oldRoot, rel) {
			if checkOnly {
				return fmt.Errorf("%s still points at %s", link, target)
			}
			if err := os.Remove(link); err != nil {
				return err
			}
			if err := os.Symlink(path, link); err != nil {
				return err
			}
		}
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
}

// removeEmptyDirs removes dir and its subdirectories bottom-up when they
// contain no files. Anything non-empty is left in place.
func removeEmptyDirs(dir string) error {
	var dirs []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) && path == dir {
			return filepath.SkipAll
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// Never descend into a worktree that stayed put (locked, with
			// submodules, or whose move failed): its empty directories are
			// the user's, not leftovers of the migration.
			if path != dir && exists(filepath.Join(path, ".git")) {
				return filepath.SkipDir
			}
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, d := range dirs {
		if emptyOrMissing(d) {
			if err := os.Remove(d); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

// hasSubmodules mirrors git's refusal to move worktrees with submodules: a
// modules directory in the worktree's administrative directory, or a
// populated gitlink recorded in .gitmodules.
func hasSubmodules(worktree string) bool {
	if admin, err := git.WorktreeAdminDir(worktree); err == nil && isDir(filepath.Join(admin, "modules")) {
		return true
	}
	data, err := os.ReadFile(filepath.Join(worktree, ".gitmodules"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(key) == "path" && exists(filepath.Join(worktree, strings.TrimSpace(value), ".git")) {
			return true
		}
	}
	return false
}

func setupRunning(worktree string) bool {
	state, err := project.ReadSetupState(worktree)
	return err == nil && state != nil && state.Status == project.SetupRunning && project.IsProcessAlive(state.PID)
}
