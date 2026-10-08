package ui

import (
	"os"
	"path/filepath"
	"strings"
)

// DisplayPath formats path for terminal output. A path inside root (or root
// itself) is shown relative to root. Any other path is shown absolute, with
// the user's home directory abbreviated to "~". An empty root skips the
// relative form. Paths are canonicalized first so symlinked spellings (macOS
// /var vs /private/var) compare equal. Loops should use NewPathDisplay to
// canonicalize root and home once.
func DisplayPath(root, path string) string {
	return NewPathDisplay(root).Path(path)
}

// PathDisplay formats paths like DisplayPath with root and the home
// directory canonicalized once.
type PathDisplay struct {
	root, home string
}

// NewPathDisplay returns a displayer for paths relative to root (empty for
// none).
func NewPathDisplay(root string) PathDisplay {
	var d PathDisplay
	if root != "" {
		d.root = CanonicalPath(root)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		d.home = CanonicalPath(home)
	}
	return d
}

// Path formats path; see DisplayPath.
func (d PathDisplay) Path(path string) string {
	if path == "" {
		return ""
	}
	p := CanonicalPath(path)
	if d.root != "" {
		if rel, ok := RelWithin(d.root, p); ok {
			return rel
		}
	}
	if d.home != "" {
		if rel, ok := RelWithin(d.home, p); ok {
			if rel == "." {
				return "~"
			}
			return "~" + string(filepath.Separator) + rel
		}
	}
	return p
}

// RelWithin returns path relative to base when path is base (".") or below
// it. It compares the paths lexically; canonicalize them first when
// symlinked spellings must match.
func RelWithin(base, path string) (string, bool) {
	rel, err := filepath.Rel(base, path)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// Within reports whether path is base or lies below it (lexically).
func Within(base, path string) bool {
	_, ok := RelWithin(base, path)
	return ok
}

// CanonicalPath returns p as an absolute path with symlinks resolved in its
// longest existing prefix (the rest is re-appended), so a missing worktree
// under a symlinked root still compares equal to the resolved root.
func CanonicalPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	rest := ""
	for dir := p; ; {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}
