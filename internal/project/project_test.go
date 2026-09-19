package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkildow/wtx/internal/config"
)

func TestRepoNameFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"git@github.com:org/repo.git", "repo"},
		{"git@github.com:org/repo", "repo"},
		{"https://github.com/org/repo.git", "repo"},
		{"https://github.com/org/repo", "repo"},
		{"git@gitlab.com:group/subgroup/repo.git", "repo"},
		{"https://gitlab.com/group/subgroup/repo.git", "repo"},
		{"git@github.com:user/my-project.git", "my-project"},
		{"https://github.com/user/my-project", "my-project"},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := RepoNameFromURL(tt.url)
			if got != tt.want {
				t.Errorf("RepoNameFromURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestFindRoot(t *testing.T) {
	// Create a nested directory structure with config at the root
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write config file at root
	cfg := config.DefaultConfig()
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}

	// FindRoot from nested dir should find root
	found, err := FindRoot(nested)
	if err != nil {
		t.Fatalf("FindRoot error: %v", err)
	}
	if found != root {
		t.Errorf("FindRoot = %q, want %q", found, root)
	}
}

func TestFindRootNotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := FindRoot(dir)
	if !errors.Is(err, config.ErrConfigNotFound) {
		t.Errorf("err = %v, want ErrConfigNotFound", err)
	}
}

func TestCreateScaffold(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{WorktreeDir: config.DefaultWorktreeDir, SharedDir: config.DefaultSharedDir}

	if err := CreateScaffold(root, cfg, false); err != nil {
		t.Fatalf("CreateScaffold error: %v", err)
	}

	dirs := []string{
		filepath.Join(root, "shared", "copy"),
		filepath.Join(root, "shared", "symlink"),
		filepath.Join(root, "worktrees"),
		filepath.Join(root, "bin"),
	}

	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("directory %q not created: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", dir)
		}
	}
}

func TestCreateScaffoldDryRun(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{WorktreeDir: config.DefaultWorktreeDir, SharedDir: config.DefaultSharedDir}

	if err := CreateScaffold(root, cfg, true); err != nil {
		t.Fatalf("CreateScaffold dry-run error: %v", err)
	}

	// In dry-run, directories should NOT be created
	dirs := []string{
		filepath.Join(root, "shared"),
		filepath.Join(root, "worktrees"),
	}

	for _, dir := range dirs {
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("dry-run should not create %q", dir)
		}
	}
}

func TestGitDirPath(t *testing.T) {
	cfg := &config.Config{GitDir: ".bare"}
	root := "/home/user/project"
	got := GitDirPath(root, cfg)
	want := filepath.Join(root, ".bare")
	if got != want {
		t.Errorf("GitDirPath = %q, want %q", got, want)
	}
}

func TestWorktreesPath(t *testing.T) {
	cfg := &config.Config{WorktreeDir: "trees"}
	root := "/home/user/project"
	got := WorktreesPath(root, cfg)
	want := filepath.Join(root, "trees")
	if got != want {
		t.Errorf("WorktreesPath = %q, want %q", got, want)
	}
}

func TestCreateScaffoldCustomDir(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{WorktreeDir: "trees", SharedDir: config.DefaultSharedDir}

	if err := CreateScaffold(root, cfg, false); err != nil {
		t.Fatalf("CreateScaffold error: %v", err)
	}

	// Custom dir should exist
	info, err := os.Stat(filepath.Join(root, "trees"))
	if err != nil {
		t.Fatalf("custom worktree dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("custom worktree dir is not a directory")
	}

	// Default "worktrees" should NOT exist
	if _, err := os.Stat(filepath.Join(root, "worktrees")); err == nil {
		t.Error("default 'worktrees' dir should not be created when custom dir is set")
	}
}

func TestBinPath(t *testing.T) {
	tests := []struct {
		name      string
		sharedDir string
		want      string
	}{
		{"clone layout", "shared", "/p/bin"},
		{"init layout", ".worktrees/shared", "/p/.worktrees/bin"},
		{"nested custom", "infra/wt/shared", "/p/infra/wt/bin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BinPath("/p", &config.Config{SharedDir: tt.sharedDir})
			if got != filepath.FromSlash(tt.want) {
				t.Errorf("BinPath = %q, want %q", got, tt.want)
			}
		})
	}
}
