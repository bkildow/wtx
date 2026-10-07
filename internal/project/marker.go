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

// MarkerFileName is the file in ~/.wtx/<name>/ recording which repository
// owns that directory.
const MarkerFileName = "project.yml"

// Marker is the content of a ~/.wtx/<name>/project.yml file.
type Marker struct {
	// Root is the absolute path of the repository (where .worktree.yml
	// lives) that owns this directory.
	Root string `yaml:"root"`
}

// ErrHomeDirTaken is returned by CheckHomeDir when a ~/.wtx/<name>
// directory belongs to another repository or was not created by wtx.
var ErrHomeDirTaken = errors.New("wtx home directory is not available")

// ValidateProjectName checks a name used for ~/.wtx/<name>.
func ValidateProjectName(name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return fmt.Errorf("invalid project name %q", name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("invalid project name %q: must not contain path separators", name)
	}
	return nil
}

// HomeProjectDir returns the absolute ~/.wtx/<name> directory (honoring
// WTX_HOME).
func HomeProjectDir(name string) (string, error) {
	if err := ValidateProjectName(name); err != nil {
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

// CheckHomeDir reports whether dir can be used for the repository at root:
// it must not exist yet, or hold a marker naming the same root. Otherwise
// the error wraps ErrHomeDirTaken.
func CheckHomeDir(dir, root string) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	m, err := ReadMarker(dir)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s exists but has no %s", ErrHomeDirTaken, dir, MarkerFileName)
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrHomeDirTaken, err)
	}
	if !SamePath(m.Root, root) {
		return fmt.Errorf("%w: %s belongs to %s", ErrHomeDirTaken, dir, m.Root)
	}
	return nil
}

// RootFromMarker recognizes startDir inside a ~/.wtx/<name>/ directory (for
// example its shared/ or bin/ folder) and returns the project root recorded
// in that directory's marker. Only direct children of the wtx home are
// considered, and the recorded root must still hold a .worktree.yml.
func RootFromMarker(startDir string) (string, bool) {
	home, err := WtxHome()
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(ui.CanonicalPath(home), ui.CanonicalPath(startDir))
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	name := strings.SplitN(rel, string(filepath.Separator), 2)[0]
	m, err := ReadMarker(filepath.Join(home, name))
	if err != nil || !config.Exists(m.Root) {
		return "", false
	}
	return m.Root, true
}
