package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bkildow/wtx/internal/config"
)

func TestExpandPath(t *testing.T) {
	home := t.TempDir()
	wtxHome := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(WtxHomeEnv, wtxHome)

	root := "/proj"
	tests := []struct {
		in   string
		want string
	}{
		{"~/.wtx/myrepo/worktrees", filepath.Join(wtxHome, "myrepo", "worktrees")},
		{"~/.wtx", wtxHome},
		{"~/.wtx/", wtxHome},
		{"~/.wtxother/x", filepath.Join(home, ".wtxother", "x")},
		{"~/code/wt", filepath.Join(home, "code", "wt")},
		{"~", home},
		{"~bob/x", filepath.Join(root, "~bob", "x")},
		{"/srv/wt", "/srv/wt"},
		{"/srv/../srv/wt/", "/srv/wt"},
		{"worktrees", filepath.Join(root, "worktrees")},
		{".worktrees/shared", filepath.Join(root, ".worktrees", "shared")},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ExpandPath(root, tt.in)
			if err != nil {
				t.Fatalf("ExpandPath(%q) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ExpandPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExpandPathWithoutWtxHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(WtxHomeEnv, "")

	got, err := ExpandPath("/proj", "~/.wtx/myrepo/shared")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".wtx", "myrepo", "shared"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestExpandPathNoHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv(WtxHomeEnv, "")

	if _, err := ExpandPath("/proj", "~/x"); err == nil {
		t.Error("expected error expanding ~ without a home directory")
	}
	if _, err := ExpandPath("/proj", "~/.wtx/x"); err == nil {
		t.Error("expected error expanding ~/.wtx without HOME or WTX_HOME")
	}
	if got, err := ExpandPath("/proj", "worktrees"); err != nil || got != "/proj/worktrees" {
		t.Errorf("relative path = %q, %v", got, err)
	}

	cfg := &config.Config{WorktreeDir: "~/.wtx/x/worktrees", SharedDir: "~/.wtx/x/shared"}
	if err := ValidatePaths("/proj", cfg); err == nil {
		t.Error("ValidatePaths should fail without HOME or WTX_HOME")
	}

	// WTX_HOME alone is enough for ~/.wtx paths.
	t.Setenv(WtxHomeEnv, t.TempDir())
	if err := ValidatePaths("/proj", cfg); err != nil {
		t.Errorf("ValidatePaths with WTX_HOME: %v", err)
	}
}

func TestPathHelpersExpand(t *testing.T) {
	wtxHome := t.TempDir()
	t.Setenv(WtxHomeEnv, wtxHome)

	cfg := &config.Config{WorktreeDir: "~/.wtx/r/worktrees", SharedDir: "~/.wtx/r/shared"}
	if got, want := WorktreesPath("/proj", cfg), filepath.Join(wtxHome, "r", "worktrees"); got != want {
		t.Errorf("WorktreesPath = %q, want %q", got, want)
	}
	if got, want := SharedPath("/proj", cfg), filepath.Join(wtxHome, "r", "shared"); got != want {
		t.Errorf("SharedPath = %q, want %q", got, want)
	}
	if got, want := BinPath("/proj", cfg), filepath.Join(wtxHome, "r", "bin"); got != want {
		t.Errorf("BinPath = %q, want %q", got, want)
	}

	abs := &config.Config{WorktreeDir: "/srv/wt", SharedDir: "/srv/shared"}
	if got := WorktreesPath("/proj", abs); got != "/srv/wt" {
		t.Errorf("absolute WorktreesPath = %q", got)
	}
	if got := BinPath("/proj", abs); got != "/srv/bin" {
		t.Errorf("absolute BinPath = %q", got)
	}
}

func TestResolveScriptTilde(t *testing.T) {
	wtxHome := t.TempDir()
	t.Setenv(WtxHomeEnv, wtxHome)

	bin := filepath.Join(wtxHome, "r", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(bin, "refresh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{Scripts: map[string]string{"refresh": "~/.wtx/r/bin/refresh"}}
	got, err := ResolveScript(cfg, t.TempDir(), "refresh")
	if err != nil {
		t.Fatalf("ResolveScript error: %v", err)
	}
	if got != script {
		t.Errorf("ResolveScript = %q, want %q", got, script)
	}
}

func TestConfigPaths(t *testing.T) {
	if got, want := HomeConfigPaths("proj"), (ConfigPaths{"~/.wtx/proj/worktrees", "~/.wtx/proj/shared", "~/.wtx/proj/bin"}); got != want {
		t.Errorf("HomeConfigPaths = %+v, want %+v", got, want)
	}
	if got, want := InRepoConfigPaths(), (ConfigPaths{".worktrees", ".worktrees/shared", ".worktrees/bin"}); got != want {
		t.Errorf("InRepoConfigPaths = %+v, want %+v", got, want)
	}
	if got := BinFor("/a/b/shared"); got != "/a/b/bin" {
		t.Errorf("BinFor = %q", got)
	}
}
