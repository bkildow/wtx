package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bkildow/wtx/internal/config"
)

// WtxHomeEnv names the environment variable that overrides the ~/.wtx
// directory holding machine-local project state.
const WtxHomeEnv = "WTX_HOME"

// wtxHomePrefix is the config spelling of the wtx home directory. Config
// values starting with it expand against WTX_HOME when that is set.
const wtxHomePrefix = "~/.wtx"

// WtxHome returns the directory holding machine-local wtx state: $WTX_HOME
// when set, otherwise ~/.wtx.
func WtxHome() (string, error) {
	if h := os.Getenv(WtxHomeEnv); h != "" {
		return filepath.Abs(h)
	}
	home, err := userHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".wtx"), nil
}

func userHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("cannot determine home directory (set HOME, or %s for ~/.wtx paths)", WtxHomeEnv)
	}
	return home, nil
}

// ExpandPath resolves a path from .worktree.yml to an absolute path:
//
//   - "~/.wtx" or "~/.wtx/..." expands against WtxHome (WTX_HOME replaces
//     the ~/.wtx prefix when set),
//   - "~" or "~/..." expands against the user's home directory,
//   - absolute paths are returned cleaned,
//   - anything else is joined onto projectRoot.
//
// "~user" forms are not expanded and are treated as relative paths.
func ExpandPath(projectRoot, p string) (string, error) {
	p = filepath.FromSlash(p)
	sep := string(filepath.Separator)
	prefix := filepath.FromSlash(wtxHomePrefix)

	switch {
	case p == prefix || strings.HasPrefix(p, prefix+sep):
		home, err := WtxHome()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(p, prefix)), nil
	case p == "~" || strings.HasPrefix(p, "~"+sep):
		home, err := userHome()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
	case filepath.IsAbs(p):
		return filepath.Clean(p), nil
	default:
		return filepath.Join(projectRoot, p), nil
	}
}

// ExpandOrJoin is ExpandPath for the string-returning path helpers. Callers
// are expected to have run ValidatePaths (loadProject does), so an error
// here means the environment changed mid-run; fall back to joining onto the
// root rather than returning an empty path.
func ExpandOrJoin(projectRoot, p string) string {
	if expanded, err := ExpandPath(projectRoot, p); err == nil {
		return expanded
	}
	return filepath.Join(projectRoot, p)
}

// ConfigPaths holds the .worktree.yml spellings of a project's worktree, shared
// and bin directories.
type ConfigPaths struct {
	WorktreeDir string
	SharedDir   string
	Bin         string
}

// HomeConfigPaths are the config paths of a Clone kept in its Clone dir
// ~/.wtx/<name>/, spelled with a literal ~ so .worktree.yml stays portable.
func HomeConfigPaths(name string) ConfigPaths {
	dir := wtxHomePrefix + "/" + name
	return newConfigPaths(dir+"/worktrees", dir+"/shared")
}

// InRepoConfigPaths are the config paths of an In-repo layout: .worktrees/
// inside the repository (wtx init --in-repo).
func InRepoConfigPaths() ConfigPaths {
	return newConfigPaths(".worktrees", ".worktrees/shared")
}

func newConfigPaths(worktreeDir, sharedDir string) ConfigPaths {
	return ConfigPaths{WorktreeDir: worktreeDir, SharedDir: sharedDir, Bin: filepath.ToSlash(BinFor(sharedDir))}
}

// BinFor returns the bin directory for a shared directory: its sibling
// named bin. It works on config spellings and expanded paths alike.
func BinFor(sharedDir string) string {
	return filepath.Join(filepath.Dir(sharedDir), "bin")
}

// ValidatePaths checks that the configured worktree and shared directories
// can be expanded (e.g. a "~" path needs a home directory or WTX_HOME).
func ValidatePaths(projectRoot string, cfg *config.Config) error {
	for _, field := range []struct{ key, value string }{
		{"worktree_dir", cfg.WorktreeDir},
		{"shared_dir", cfg.SharedDir},
	} {
		if _, err := ExpandPath(projectRoot, field.value); err != nil {
			return fmt.Errorf("%s %q: %w", field.key, field.value, err)
		}
	}
	return nil
}
