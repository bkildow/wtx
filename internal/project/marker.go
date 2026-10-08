package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/ui"
)

// MarkerFileName is the Owner marker file in a Clone dir, recording the
// Project root that owns it.
const MarkerFileName = "project.yml"

// Marker is the content of an Owner marker (<Clone dir>/project.yml).
type Marker struct {
	// Root is the absolute path of the repository (where .worktree.yml
	// lives) that owns this directory.
	Root string `yaml:"root"`
}

// ErrCloneDirTaken is returned by CheckCloneDir when a Clone dir belongs to
// another Project root or was not created by wtx.
var ErrCloneDirTaken = errors.New("clone directory is not available")

// ValidateCloneName checks a Clone name, which names a Clone dir.
func ValidateCloneName(name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return fmt.Errorf("invalid clone name %q", name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("invalid clone name %q: must not contain path separators", name)
	}
	return nil
}

// CloneDirFor returns the absolute Clone dir for a Clone name:
// ~/.wtx/<name>, honoring WTX_HOME.
func CloneDirFor(name string) (string, error) {
	if err := ValidateCloneName(name); err != nil {
		return "", err
	}
	home, err := WtxHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, name), nil
}

// ReadMarker reads the marker in dir. A missing marker is reported as an
// error wrapping os.ErrNotExist.
func ReadMarker(dir string) (Marker, error) {
	var m Marker
	data, err := os.ReadFile(filepath.Join(dir, MarkerFileName))
	if err != nil {
		return m, err
	}
	if err := yaml.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parse %s: %w", filepath.Join(dir, MarkerFileName), err)
	}
	if m.Root == "" {
		return m, fmt.Errorf("%s has no root", filepath.Join(dir, MarkerFileName))
	}
	return m, nil
}

// WriteMarker records root as the owner of dir, creating dir if needed. In
// dry-run mode it only prints what it would do.
func WriteMarker(dir, root string, dryRun bool) error {
	path := filepath.Join(dir, MarkerFileName)
	if dryRun {
		ui.DryRunNotice("mkdir -p " + dir)
		ui.DryRunNotice("write " + path)
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content, err := MarkerContent(root)
	if err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// MarkerContent returns the marker file content recording root as owner.
func MarkerContent(root string) ([]byte, error) {
	data, err := yaml.Marshal(Marker{Root: root})
	if err != nil {
		return nil, err
	}
	return append([]byte("# Written by wtx: the repository that owns this directory.\n"), data...), nil
}

// SamePath reports whether a and b name the same location once symlinks in
// their existing prefixes are resolved.
func SamePath(a, b string) bool {
	return ui.CanonicalPath(a) == ui.CanonicalPath(b)
}

// CheckCloneDir reports whether dir can be the Clone dir of the Project root
// root: it must not exist yet, or hold an Owner marker naming the same root.
// Otherwise the error wraps ErrCloneDirTaken.
func CheckCloneDir(dir, root string) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	m, err := ReadMarker(dir)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s exists but has no %s", ErrCloneDirTaken, dir, MarkerFileName)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCloneDirTaken, err)
	}
	if !SamePath(m.Root, root) {
		return fmt.Errorf("%w: %s belongs to %s", ErrCloneDirTaken, dir, m.Root)
	}
	return nil
}

// CloneDirOf returns the Clone dir (canonical, honoring WTX_HOME) that path
// lies in. The wtx home itself and paths outside it
// report false.
func CloneDirOf(path string) (string, bool) {
	home, err := WtxHome()
	if err != nil {
		return "", false
	}
	home = ui.CanonicalPath(home)
	rel, ok := ui.RelWithin(home, ui.CanonicalPath(path))
	if !ok || rel == "." {
		return "", false
	}
	return filepath.Join(home, strings.SplitN(rel, string(filepath.Separator), 2)[0]), true
}

// SelectCloneDir picks the Clone dir named name for the Project root root. An empty dir means name is invalid (including empty) or the wtx home
// cannot be determined. A non-empty dir with an error means the directory is
// not available to root (see CheckCloneDir).
func SelectCloneDir(root, name string) (dir string, err error) {
	if dir, err = CloneDirFor(name); err != nil {
		return "", err
	}
	return dir, CheckCloneDir(dir, root)
}

// RootFromMarker recognizes startDir inside a Clone dir (for example its
// shared/ or bin/ folder) and returns the Project root its Owner marker
// records. Only direct children of the wtx home are
// considered, and the recorded root must still hold a .worktree.yml.
func RootFromMarker(startDir string) (string, bool) {
	dir, ok := CloneDirOf(startDir)
	if !ok {
		return "", false
	}
	m, err := ReadMarker(dir)
	if err != nil || !config.Exists(m.Root) {
		return "", false
	}
	return m.Root, true
}
