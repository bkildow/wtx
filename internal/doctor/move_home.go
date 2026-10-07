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

// migrateID is the finding and repair ID of the opt-in move of an in-repo
// init project (.worktrees/) to ~/.wtx/<name>/.
const migrateID = "home.migrate"

// migration is the plan for one --migrate-home run. Source directories are
// empty when there is nothing left to move, which makes a re-run after a
// partial failure pick up where the previous run stopped.
type migration struct {
	s        *inspection
	finding  int
	homeDir  string // ~/.wtx/<name>, expanded
	name     string
	oldWT    string // in-repo worktree directory, or ""
	oldShare string // in-repo shared directory, or ""
	oldBin   string // in-repo bin directory, or ""
	newWT    string
	newShare string
	newBin   string
	moved    []string // worktree destinations
	steps    []repair
}

// planMigration plans moving an in-repo init project to ~/.wtx/<name>/.
// Every step is an operation repair that re-checks its own preconditions, so
// the plan is safe to apply after a partial failure and is a no-op once done.
func (s *inspection) planMigration(ctx context.Context, worktrees []git.WorktreeInfo, name string) {
	cfgPath := filepath.Join(s.root, config.ConfigFileName)
	if s.cfg.GitDir != ".git" {
		s.add(migrateID, Warn, cfgPath, "--migrate-home applies only to projects set up with wtx init (git_dir: .git); this project keeps its layout.", "")
		return
	}
	if err := project.ValidatePaths(s.root, s.cfg); err != nil {
		s.problem(migrateID, cfgPath, err)
		return
	}
	m := &migration{s: s}
	inRepo := func(p string) bool { return p != s.root && within(s.root, p) }
	wtDir := ui.CanonicalPath(project.WorktreesPath(s.root, s.cfg))
	shareDir := ui.CanonicalPath(project.SharedPath(s.root, s.cfg))
	binDir := ui.CanonicalPath(project.BinPath(s.root, s.cfg))
	legacy := filepath.Join(s.root, legacyWorktreeDir)

	// Sources: the configured directories while they are in the repository,
	// otherwise what an interrupted migration left in .worktrees/.
	if inRepo(wtDir) {
		m.oldWT = wtDir
	} else if isDir(legacy) {
		m.oldWT = legacy
	}
	if inRepo(shareDir) {
		m.oldShare, m.oldBin = shareDir, binDir
	}
	if m.oldShare != "" && (m.oldWT == "" || !within(m.oldWT, m.oldShare) || !within(m.oldWT, m.oldBin)) {
		s.add(migrateID, Fail, cfgPath, "shared_dir is inside the repository but not under worktree_dir; wtx cannot tell which files are its own.", "Move the shared and bin directories manually and update shared_dir and scripts in "+config.ConfigFileName+".")
		return
	}

	// Destination: the ~/.wtx/<name> directory the config already names
	// (re-run), else --name or the repository directory name.
	if dirs := s.homeProjectDirs(); len(dirs) > 0 {
		m.homeDir = dirs[0]
	} else {
		if name == "" {
			name = filepath.Base(s.root)
		}
		dir, err := project.HomeProjectDir(name)
		if err != nil {
			s.add(migrateID, Fail, cfgPath, err.Error()+".", "Choose a name with wtx doctor --migrate-home --name <name>.")
			return
		}
		m.homeDir = ui.CanonicalPath(dir)
	}
	m.name = filepath.Base(m.homeDir)
	if err := project.CheckHomeDir(m.homeDir, s.root); err != nil {
		s.add(migrateID, Fail, m.homeDir, err.Error()+".", "Choose another directory name with wtx doctor --migrate-home --name <name>.")
		return
	}
	m.newWT, m.newShare = filepath.Join(m.homeDir, "worktrees"), filepath.Join(m.homeDir, "shared")
	if !inRepo(wtDir) {
		m.newWT = wtDir
	}
	if !inRepo(shareDir) {
		m.newShare = shareDir
	}
	m.newBin = filepath.Join(filepath.Dir(m.newShare), "bin")
	s.addManaged(m.homeDir)

	if m.oldWT == "" && m.oldShare == "" {
		s.add(migrateID, OK, m.homeDir, "Worktrees and shared files already live outside the repository.", "")
		return
	}
	if cwd, err := os.Getwd(); err == nil && m.oldWT != "" && within(m.oldWT, ui.CanonicalPath(cwd)) {
		s.add(migrateID, Fail, cwd, "The current directory is inside "+m.oldWT+", which the migration moves.", "Run wtx doctor --migrate-home from the main checkout: "+s.root)
		return
	}
	from := m.oldWT
	if from == "" {
		from = filepath.Dir(m.oldShare)
	}
	m.finding = s.add(migrateID, Warn, m.homeDir, "Worktrees, shared files and scripts move from "+ui.DisplayPath(s.root, from)+" to "+ui.DisplayPath("", m.homeDir)+".", "Planned steps are listed under Repairs; --dry-run previews them without changing anything.")

	m.planMarker()
	keepShare := m.planDir(ctx, "shared", m.oldShare, m.newShare)
	// bin/ is resolved as a sibling of shared_dir, so it stays wherever shared/ stays.
	keepBin := keepShare
	if keepShare && m.oldBin != "" && exists(m.oldBin) {
		m.skip(m.oldBin, "bin", "The bin directory stays next to the shared directory, which stays in the repository.", "Untrack the shared files and rerun wtx doctor --migrate-home to move both.")
	} else if !keepShare {
		keepBin = m.planDir(ctx, "bin", m.oldBin, m.newBin)
	}
	m.planWorktrees(worktrees)
	if m.oldShare != "" && !keepShare {
		m.planRelink()
	}
	if len(m.moved) > 0 {
		m.planRepair()
	}
	if m.oldWT != "" && !keepShare && !keepBin {
		m.planCleanup()
	}
	if len(m.steps) > 0 || inRepo(wtDir) || (inRepo(shareDir) && !keepShare) {
		m.planConfig(inRepo(wtDir), inRepo(shareDir) && !keepShare, m.oldBin != "" && !keepBin)
	}
	if len(m.steps) > 0 {
		s.report.Findings[m.finding].Repairable = true
	}
}

func (m *migration) step(target, action string, run func(context.Context) error, done func() error) {
	r := repair{id: migrateID, file: snapshot{path: target}, action: action, run: run, done: done, backups: m.s.backups}
	r.guards = append([]snapshot(nil), m.s.guards...)
	m.s.repairs = append(m.s.repairs, r)
	m.steps = append(m.steps, r)
}

func (m *migration) skip(path, subject, explanation, remedy string) {
	i := m.s.add(migrateID, Warn, path, explanation, remedy)
	m.s.report.Findings[i].Subject = subject
}

func (m *migration) planMarker() {
	if mk, err := project.ReadMarker(m.homeDir); err == nil && project.SamePath(mk.Root, m.s.root) {
		return
	}
	root := m.s.root
	m.step(filepath.Join(m.homeDir, project.MarkerFileName), "write ownership marker",
		func(context.Context) error {
			if err := project.CheckHomeDir(m.homeDir, root); err != nil {
				return err
			}
			return project.WriteMarker(m.homeDir, root, false)
		},
		func() error {
			mk, err := project.ReadMarker(m.homeDir)
			if err == nil && !project.SamePath(mk.Root, root) {
				err = fmt.Errorf("marker names %s", mk.Root)
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
	rel, _ := filepath.Rel(m.s.root, from)
	if out, err := m.s.runner.QueryRaw(ctx, "-C", m.s.root, "ls-files", "-z", "--", rel); err != nil {
		m.s.problem(migrateID, from, err)
		return true
	} else if out != "" {
		m.skip(from, label, "The "+label+" directory contains files tracked by Git; it stays in the repository.", "Keep it, or untrack it and rerun wtx doctor --migrate-home to move it.")
		return true
	}
	if !emptyOrMissing(to) {
		m.skip(to, label, "Destination "+label+" directory already exists and is not empty.", "Merge "+from+" into it manually, then rerun wtx doctor --migrate-home.")
		return true
	}
	m.step(to, "move "+m.s.display(from)+" → "+ui.DisplayPath("", to),
		func(context.Context) error { return moveDir(from, to) },
		func() error { return movedCheck(from, to) })
	return false
}

func (m *migration) planWorktrees(worktrees []git.WorktreeInfo) {
	if m.oldWT == "" {
		return
	}
	for _, wt := range worktrees {
		src := ui.CanonicalPath(wt.Path)
		if wt.Bare || src == m.s.root || !within(m.oldWT, src) ||
			(m.oldShare != "" && within(m.oldShare, src)) || (m.oldBin != "" && within(m.oldBin, src)) {
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
			m.skip(wt.Path, wt.Branch, "Worktree is locked; it was not moved.", fmt.Sprintf("Unlock it with git worktree unlock %q, then rerun wtx doctor --migrate-home.", wt.Path))
			continue
		case wt.Prunable || !isDir(src):
			m.skip(wt.Path, wt.Branch, "Worktree directory is missing or prunable; it was not moved.", "Review git worktree prune --dry-run, then rerun wtx doctor --migrate-home.")
			continue
		case hasSubmodules(src):
			m.skip(wt.Path, wt.Branch, "Worktree has submodules, which git worktree move cannot move; it was not moved.", "Remove it with wtx remove and recreate it with wtx add, or move it manually.")
			continue
		case setupRunning(src):
			m.skip(wt.Path, wt.Branch, "Setup is still running in this worktree; it was not moved.", "Wait for setup to finish, then rerun wtx doctor --migrate-home.")
			continue
		case exists(dest):
			m.skip(dest, wt.Branch, "Destination already exists; the worktree was not moved.", "Move or remove "+dest+", then rerun wtx doctor --migrate-home.")
			continue
		}
		m.moved = append(m.moved, dest)
		runner := m.s.runner
		m.step(dest, "git worktree move "+m.s.display(src)+" → "+ui.DisplayPath("", dest),
			func(ctx context.Context) error {
				if !isDir(src) {
					return fmt.Errorf("worktree is no longer at %s", src)
				}
				if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
					return err
				}
				_, err := runner.Run(ctx, "worktree", "move", src, dest)
				return err
			},
			func() error { return movedCheck(src, filepath.Join(dest, ".git")) })
	}
}

// planRelink retargets managed symlinks in every linked worktree from the
// old shared/symlink tree to the new one.
func (m *migration) planRelink() {
	oldLinks, newLinks := filepath.Join(m.oldShare, "symlink"), filepath.Join(m.newShare, "symlink")
	runner := m.s.runner
	root := m.s.root
	m.step(newLinks, "retarget shared symlinks in worktrees",
		func(ctx context.Context) error {
			if !isDir(newLinks) {
				return nil
			}
			worktrees, err := runner.WorktreeList(ctx)
			if err != nil {
				return err
			}
			var errs []error
			for _, wt := range worktrees {
				if !wt.Bare && ui.CanonicalPath(wt.Path) != root && isDir(wt.Path) {
					errs = append(errs, relink(newLinks, oldLinks, wt.Path))
				}
			}
			return errors.Join(errs...)
		}, nil)
}

func (m *migration) planRepair() {
	runner, moved := m.s.runner, m.moved
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
		args := []string{"worktree", "repair"}
		// Keep relative links, as wtx add creates them, where Git supports it.
		if v, err := runner.Version(ctx); err == nil && (v[0] > 2 || (v[0] == 2 && v[1] >= 48)) {
			args = append(args, "--relative-paths")
		}
		_, err := runner.Run(ctx, append(args, present...)...)
		return err
	}, nil)
}

func (m *migration) planCleanup() {
	dir := m.oldWT
	m.step(dir, "remove "+m.s.display(dir)+" if empty", func(context.Context) error {
		return removeEmptyDirs(dir)
	}, nil)
}

// planConfig rewrites worktree_dir, shared_dir and scripts that pointed into
// the old bin directory, using the portable ~/.wtx/<name>/... spelling. It
// runs last (every step checks the config is unchanged) and only once every
// earlier step is done.
func (m *migration) planConfig(wt, shared, bin bool) {
	path := filepath.Join(m.s.root, config.ConfigFileName)
	file, err := takeSnapshot(path)
	if err != nil {
		m.s.problem(migrateID, path, err)
		return
	}
	prefix := "~/.wtx/" + m.name
	want := *m.s.cfg
	want.Scripts = maps.Clone(m.s.cfg.Scripts)
	if wt {
		want.WorktreeDir = prefix + "/worktrees"
	}
	if shared {
		want.SharedDir = prefix + "/shared"
	}
	if bin {
		for name, value := range want.Scripts {
			p, err := project.ExpandPath(m.s.root, value)
			if err != nil {
				continue
			}
			if p = ui.CanonicalPath(p); within(m.oldBin, p) {
				rel, _ := filepath.Rel(m.oldBin, p)
				want.Scripts[name] = prefix + "/bin/" + filepath.ToSlash(rel)
			}
		}
	}
	data, err := rewriteConfig(file.data, m.s.cfg, &want)
	if err != nil {
		m.s.add(migrateID, Fail, path, "Cannot rewrite "+config.ConfigFileName+" in place: "+err.Error()+".", fmt.Sprintf("Set worktree_dir: %s and shared_dir: %s (and update scripts under the old bin directory) manually.", want.WorktreeDir, want.SharedDir))
		return
	}
	if bytes.Equal(data, file.data) {
		return
	}
	steps := slices.Clone(m.steps)
	r := repair{id: migrateID, file: file, data: data, action: "rewrite worktree_dir, shared_dir and scripts with ~/.wtx paths", backups: m.s.backups}
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

// rewriteConfig edits worktree_dir, shared_dir and block-style scripts
// entries line by line, preserving comments and layout, then checks that the
// result parses to want.
func rewriteConfig(data []byte, have, want *config.Config) ([]byte, error) {
	lines := strings.Split(string(data), "\n")
	inScripts := false
	for i, line := range lines {
		if k := topLevelKey.FindStringSubmatch(line); k != nil {
			inScripts = k[1] == "scripts"
			switch {
			case k[1] == "worktree_dir" && have.WorktreeDir != want.WorktreeDir:
				lines[i] = "worktree_dir: " + config.YAMLQuote(want.WorktreeDir)
			case k[1] == "shared_dir" && have.SharedDir != want.SharedDir:
				lines[i] = "shared_dir: " + config.YAMLQuote(want.SharedDir)
			}
			continue
		}
		if !inScripts {
			continue
		}
		e := scriptEntry.FindStringSubmatch(line)
		if e == nil {
			continue
		}
		var name string
		if err := yaml.Unmarshal([]byte(e[2]), &name); err != nil {
			continue
		}
		if v, ok := want.Scripts[name]; ok && v != have.Scripts[name] {
			lines[i] = e[1] + e[2] + ": " + config.YAMLQuote(v)
		}
	}
	out := []byte(strings.Join(lines, "\n"))
	got := config.DefaultConfig()
	if err := yaml.Unmarshal(out, &got); err != nil {
		return nil, err
	}
	if got.WorktreeDir != want.WorktreeDir || got.SharedDir != want.SharedDir || !sameMap(got.Scripts, want.Scripts) {
		return nil, errors.New("unrecognized layout (flow-style or multi-line values)")
	}
	return out, nil
}

func sameMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func (s *inspection) display(path string) string {
	return ui.DisplayPath(s.root, path)
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
	if !emptyOrMissing(to) {
		return fmt.Errorf("%s already exists and is not empty", to)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if exists(to) {
		if err := os.Remove(to); err != nil {
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
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, dest)
		default:
			return fscopy.CopyFile(path, dest)
		}
	})
}

// relink retargets symlinks under worktree that point at oldRoot/<rel> to
// newRoot/<rel>, walking only the mirror of newRoot. Other links are left
// alone.
func relink(newRoot, oldRoot, worktree string) error {
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
		if err == nil && ui.CanonicalPath(target) == ui.CanonicalPath(filepath.Join(oldRoot, rel)) {
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
	if data, err := os.ReadFile(filepath.Join(worktree, ".git")); err == nil {
		if admin, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: "); ok {
			if !filepath.IsAbs(admin) {
				admin = filepath.Join(worktree, admin)
			}
			if isDir(filepath.Join(admin, "modules")) {
				return true
			}
		}
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
