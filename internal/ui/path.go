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
// /var vs /private/var) compare equal.
func DisplayPath(root, path string) string {
	if path == "" {
		return ""
	}
	p := CanonicalPath(path)
	if root != "" {
		if rel, ok := relWithin(CanonicalPath(root), p); ok {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, ok := relWithin(CanonicalPath(home), p); ok {
			if rel == "." {
				return "~"
			}
			return "~" + string(filepath.Separator) + rel
		}
	}
	return p
}

// relWithin returns path relative to base when path is base or below it.
func relWithin(base, path string) (string, bool) {
	rel, err := filepath.Rel(base, path)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
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
