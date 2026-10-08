package doctor

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
)

// migrateHint names the opt-in migration for in-repo projects.
const migrateHint = "wtx doctor --migrate-home (preview with --dry-run)"

// homePaths checks that the worktree directory exists (or can be created)
// and is writable; inspect reports worktree_dir and shared_dir that do not
// expand. It also records directories outside the root that hold managed
// files, so external worktrees are not treated as foreign.
func (s *inspection) homePaths() {
	wt := s.clone.WorktreesDir()
	for _, dir := range []string{wt, s.clone.SharedDir(), s.clone.BinDir()} {
		s.addManaged(dir)
	}
	info, err := os.Stat(wt)
	switch {
	case errors.Is(err, os.ErrNotExist):
		parent := existingAncestor(wt)
		if unix.Access(parent, unix.W_OK) != nil {
			s.add("home.paths", Fail, wt, "Worktree directory does not exist and "+parent+" is not writable.", "Create the directory or fix permissions, or change worktree_dir in "+config.ConfigFileName+".")
			return
		}
		if dir, ok := s.clone.CloneDir(); ok && !exists(filepath.Join(dir, project.MarkerFileName)) {
			s.add("home.paths", OK, wt, "Worktree directory does not exist yet; wtx init sets up this clone ("+s.clone.Name().Describe()+").", "")
			return
		}
		s.add("home.paths", OK, wt, "Worktree directory does not exist yet; wtx add creates it.", "")
	case err != nil:
		s.problem("home.paths", wt, err)
	case !info.IsDir():
		s.add("home.paths", Fail, wt, "Worktree directory path is not a directory.", "Move the file aside or change worktree_dir in "+config.ConfigFileName+".")
	case unix.Access(wt, unix.W_OK) != nil:
		s.add("home.paths", Fail, wt, "Worktree directory is not writable.", "Fix the directory permissions so wtx add can create worktrees.")
	default:
		s.add("home.paths", OK, wt, "Worktree directory exists and is writable.", "")
	}
}

// addManaged records dir as managed when it lies outside the root. A
// directory containing the root (e.g. worktree_dir: ~) is never recorded, so
// it cannot widen the project to the whole home directory.
func (s *inspection) addManaged(dir string) {
	dir = ui.CanonicalPath(dir)
	if ui.Within(s.root, dir) || ui.Within(dir, s.root) {
		return
	}
	s.managedDirs = append(s.managedDirs, dir)
}

func existingAncestor(path string) string {
	for {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

// homeMarker checks that each ~/.wtx/<name> directory this project uses
// records this project as its owner.
func (s *inspection) homeMarker() {
	cloneDir, perClone := s.clone.CloneDir()
	for _, dir := range s.clone.CloneDirs() {
		s.addManaged(dir)
		path := filepath.Join(dir, project.MarkerFileName)
		// For per-clone defaults, say where <name> came from.
		detail, otherRemedy := "", "Point worktree_dir and shared_dir at a different ~/.wtx/<name> directory and move this project's files there; doctor never changes another project's directory."
		if perClone && dir == cloneDir {
			detail = " (" + s.clone.Name().Describe() + ")"
			otherRemedy = "Run wtx init --name <other> to give this clone its own ~/.wtx directory; doctor never changes another project's directory."
		}
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			if detail != "" {
				s.add("home.marker", Warn, path, "This clone is not set up: its project directory does not exist"+detail+".", "Run wtx init to set up this clone.")
				continue
			}
			s.add("home.marker", OK, path, "Project directory does not exist yet; wtx creates it with the first worktree.", "")
			continue
		}
		content, err := project.MarkerContent(s.root)
		if err != nil {
			s.problem("home.marker", path, err)
			continue
		}
		m, err := project.ReadMarker(dir)
		switch {
		case errors.Is(err, os.ErrNotExist):
			i := s.add("home.marker", Warn, path, "Owner marker is missing"+detail+"; wtx uses it to detect name collisions and orphaned directories.", "Run wtx doctor --fix to record this project as the owner.")
			s.planMarker(i, path, content)
		case err != nil:
			i := s.add("home.marker", Warn, path, "Owner marker is unreadable: "+err.Error()+".", "Run wtx doctor --fix to rewrite it (the old file is backed up).")
			s.planMarker(i, path, content)
		case project.SamePath(m.Root, s.root):
			s.add("home.marker", OK, path, "Owner marker names this project"+detail+".", "")
		case config.Exists(m.Root):
			s.add("home.marker", Warn, path, "Directory belongs to another wtx project: "+m.Root+detail+".", otherRemedy)
		default:
			i := s.add("home.marker", Warn, path, "Owner marker names "+m.Root+", which is no longer a wtx project (moved repository?).", "Run wtx doctor --fix to record this project as the owner.")
			s.planMarker(i, path, content)
		}
	}
}

func (s *inspection) planMarker(index int, path string, content []byte) {
	file, err := takeSnapshot(path)
	if err != nil {
		s.problem("home.marker", path, err)
		return
	}
	s.planContent(index, file, content, nil)
}

// homeOrphans reports ~/.wtx/<name> directories whose recorded owner no
// longer exists or is no longer a wtx project. They are never deleted.
func (s *inspection) homeOrphans() {
	home, err := project.WtxHome()
	if err != nil {
		return
	}
	entries, err := os.ReadDir(home)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		s.problem("home.orphans", home, err)
		return
	}
	found := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(home, entry.Name())
		m, err := project.ReadMarker(dir)
		if err != nil || project.SamePath(m.Root, s.root) || config.Exists(m.Root) {
			continue // not a wtx directory, ours, or owned by a live project
		}
		found = true
		s.addSubject("home.orphans", dir, m.Root, "Owning repository "+m.Root+" no longer exists or is no longer a wtx project.", "Review its worktrees and shared files, then delete the directory manually if unneeded; doctor never deletes it.")
	}
	if !found {
		s.add("home.orphans", OK, home, "No orphaned project directories.", "")
	}
}

// homeLayout hints at the migration for in-repo projects and warns about
// worktrees left in the legacy in-repo directory after worktree_dir moved.
func (s *inspection) homeLayout(worktrees []git.WorktreeInfo) {
	wtDir := ui.CanonicalPath(s.clone.WorktreesDir())
	initLayout := s.clone.Layout() != project.BareLayout
	if s.clone.Layout() == project.InRepoLayout {
		s.add("home.layout", OK, wtDir, "Worktrees live inside the repository (in-repo layout).", "Optional: "+migrateHint+" moves worktrees, shared files and scripts to ~/.wtx/<name>/.")
		return
	}
	// The in-repo directory written by wtx init --in-repo (and by wtx init
	// before ~/.wtx became the default).
	legacyDir := project.InRepoConfigPaths().WorktreeDir
	legacy := filepath.Join(s.root, legacyDir)
	leftovers := 0
	for _, wt := range worktrees {
		path := ui.CanonicalPath(wt.Path)
		if wt.Bare || path == s.root || !ui.Within(legacy, path) {
			continue
		}
		leftovers++
		remedy := "Move it with git worktree move into the configured worktree directory."
		if initLayout {
			remedy = "Run " + migrateHint + " to move it."
		}
		s.addSubject("home.layout", wt.Path, wt.Branch, "Worktree is still in the in-repo "+legacyDir+" directory, but worktree_dir points elsewhere.", remedy)
	}
	if leftovers == 0 {
		s.add("home.layout", OK, wtDir, "Worktrees live in the configured directory.", "")
	}
}
