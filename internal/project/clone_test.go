package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
)

// checkoutConfig writes .worktree.yml with git_dir: .git plus extra lines and
// loads it.
func checkoutConfig(t *testing.T, root, extra string) *config.Config {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, config.ConfigFileName), []byte("version: 1\ngit_dir: .git\n"+extra), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestReadCloneName(t *testing.T) {
	requireGit(t)
	root := filepath.Join(t.TempDir(), "myrepo")
	initRepo(t, root, false)
	cfg := checkoutConfig(t, root, "")
	ctx := context.Background()

	got, err := ReadCloneName(ctx, root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got != (CloneName{Name: "myrepo", Source: NameFromDirectory}) {
		t.Errorf("unset wtx.name: got %+v", got)
	}

	runGit(t, "-C", root, "config", "--local", NameConfigKey, "other")
	got, err = ReadCloneName(ctx, root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got != (CloneName{Name: "other", Source: NameFromGitConfig}) {
		t.Errorf("wtx.name set: got %+v", got)
	}
	if !strings.Contains(got.Describe(), NameConfigKey) {
		t.Errorf("Describe() = %q", got.Describe())
	}

	runGit(t, "-C", root, "config", "--local", NameConfigKey, "../escape")
	if _, err := ReadCloneName(ctx, root, cfg); err == nil || !strings.Contains(err.Error(), "invalid project name") {
		t.Errorf("invalid wtx.name: err = %v", err)
	}
}

func TestReadCloneNameUnreadableGitDirFallsBack(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := checkoutConfig(t, root, "")
	got, err := ReadCloneName(context.Background(), root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "plain" || got.Source != NameFromDirectory {
		t.Errorf("got %+v", got)
	}
}

func TestResolveLayoutOmittedVsExplicit(t *testing.T) {
	requireGit(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(WtxHomeEnv, filepath.Join(home, "wtx-home"))
	root := filepath.Join(t.TempDir(), "myrepo")
	initRepo(t, root, false)
	ctx := context.Background()

	tests := []struct {
		name               string
		extra              string
		gitName            string
		wantWT, wantShared string
		wantOK             bool
	}{
		{"omitted defaults to directory name", "", "", "~/.wtx/myrepo/worktrees", "~/.wtx/myrepo/shared", true},
		{"omitted uses wtx.name", "", "teammate", "~/.wtx/teammate/worktrees", "~/.wtx/teammate/shared", true},
		{"explicit in-repo is kept", "worktree_dir: .worktrees\nshared_dir: .worktrees/shared\n", "teammate", ".worktrees", ".worktrees/shared", false},
		{"explicit home is kept", "worktree_dir: ~/.wtx/old/worktrees\nshared_dir: ~/.wtx/old/shared\n", "teammate", "~/.wtx/old/worktrees", "~/.wtx/old/shared", false},
		{"only shared omitted", "worktree_dir: trees\n", "", "trees", "~/.wtx/myrepo/shared", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.gitName != "" {
				runGit(t, "-C", root, "config", "--local", NameConfigKey, tt.gitName)
				t.Cleanup(func() { runGit(t, "-C", root, "config", "--local", "--unset", NameConfigKey) })
			}
			cfg := checkoutConfig(t, root, tt.extra)
			_, ok, err := ResolveLayout(ctx, root, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if ok != tt.wantOK || cfg.WorktreeDir != tt.wantWT || cfg.SharedDir != tt.wantShared {
				t.Errorf("got ok=%v %q %q; want ok=%v %q %q", ok, cfg.WorktreeDir, cfg.SharedDir, tt.wantOK, tt.wantWT, tt.wantShared)
			}
		})
	}

	// Bare layouts keep the plain defaults.
	if err := os.WriteFile(filepath.Join(root, config.ConfigFileName), []byte("git_dir: .bare\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := ResolveLayout(ctx, root, cfg); err != nil || ok || cfg.WorktreeDir != config.DefaultWorktreeDir || cfg.SharedDir != config.DefaultSharedDir {
		t.Errorf("bare layout: ok=%v err=%v %q %q", ok, err, cfg.WorktreeDir, cfg.SharedDir)
	}
}

func TestCheckCloneSetup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(WtxHomeEnv, filepath.Join(home, "wtx-home"))
	root := filepath.Join(t.TempDir(), "myrepo")
	other := filepath.Join(t.TempDir(), "myrepo")
	for _, dir := range []string{root, other} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := checkoutConfig(t, root, "")
	ApplyHomeLayout(cfg, "myrepo")
	dir, err := HomeProjectDir("myrepo")
	if err != nil {
		t.Fatal(err)
	}

	err = CheckCloneSetup(root, cfg)
	if !errors.Is(err, ErrCloneNotSetUp) || !strings.Contains(err.Error(), "wtx init") {
		t.Errorf("missing marker: err = %v", err)
	}

	checkoutConfig(t, other, "")
	if err := WriteMarker(dir, other, false); err != nil {
		t.Fatal(err)
	}
	err = CheckCloneSetup(root, cfg)
	if !errors.Is(err, ErrCloneNotSetUp) || !strings.Contains(err.Error(), "wtx init --name") {
		t.Errorf("other owner: err = %v", err)
	}

	if err := WriteMarker(dir, root, false); err != nil {
		t.Fatal(err)
	}
	if err := CheckCloneSetup(root, cfg); err != nil {
		t.Errorf("own marker: err = %v", err)
	}

	// Explicit paths are not checked.
	explicit := checkoutConfig(t, root, "worktree_dir: ~/.wtx/x/worktrees\nshared_dir: ~/.wtx/x/shared\n")
	if err := CheckCloneSetup(root, explicit); err != nil {
		t.Errorf("explicit paths: err = %v", err)
	}
}
