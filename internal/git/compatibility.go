package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WorktreeConfigPath resolves the administrative config of a worktree.
func WorktreeConfigPath(worktreePath string) (string, error) {
	path := filepath.Join(worktreePath, ".git")
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return filepath.Join(path, "config.worktree"), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return "", fmt.Errorf("malformed .git file at %s", path)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(worktreePath, value)
	}
	return filepath.Join(value, "config.worktree"), nil
}

// SetConfigFile uses Git's parser and lock/rename protocol. Doctor calls it on
// a staged copy before atomically replacing a guarded original.
func SetConfigFile(ctx context.Context, path, key, value string) error {
	out, err := exec.CommandContext(ctx, "git", "config", "--file", path, key, value).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git config %s: %w: %s", key, err, out)
	}
	return nil
}

// ConfigBool reads one file without following arbitrary includes.
func ConfigBool(ctx context.Context, path, key string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "config", "--file", path, "--no-includes", "--bool", "--get", key).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", nil
	}
	return strings.TrimSpace(string(out)), err
}
