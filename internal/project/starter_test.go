package project

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
)

func TestStarterScripts(t *testing.T) {
	tests := []struct {
		name      string
		sharedDir string
		want      string
	}{
		{"clone layout", "shared", "bin/refresh"},
		{"init layout", ".worktrees/shared", ".worktrees/bin/refresh"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StarterScripts("/p", &config.Config{SharedDir: tt.sharedDir})
			if got["refresh"] != tt.want {
				t.Errorf("scripts = %v, want refresh=%q", got, tt.want)
			}
		})
	}
}

func TestWriteStarterScripts(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{SharedDir: config.DefaultSharedDir}

	if err := WriteStarterScripts(root, cfg, false); err != nil {
		t.Fatalf("WriteStarterScripts error: %v", err)
	}

	dest := filepath.Join(root, "bin", "refresh")
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("starter not written: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("starter is not executable: %v", info.Mode())
	}

	content, _ := os.ReadFile(dest)
	for _, want := range []string{"#!/usr/bin/env bash", "WTX_PROJECT_ROOT", "WTX_SHARED_PATH", "WTX_MAIN_BRANCH", "WTX_MAIN_WORKTREE_PATH", "WTX_WORKTREE_PATH", "WTX_BRANCH_NAME", "WTX_WORKTREE_ID", "WTX_SCRIPT_NAME", "NOTE TO AI AGENTS", "docker compose"} {
		if !strings.Contains(string(content), want) {
			t.Errorf("starter missing %q", want)
		}
	}

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if out, err := exec.Command("bash", "-n", dest).CombinedOutput(); err != nil {
		t.Fatalf("starter does not parse: %v\n%s", err, out)
	}

	// Running the stub is a no-op that succeeds and points at itself.
	var stderr bytes.Buffer
	cmd := exec.Command(dest)
	cmd.Dir = root
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("stub exited non-zero: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "not implemented") || !strings.Contains(stderr.String(), dest) {
		t.Errorf("stub message = %q", stderr.String())
	}

	// --help works, unknown args fail.
	if out, err := exec.Command(dest, "--help").CombinedOutput(); err != nil {
		t.Errorf("--help failed: %v", err)
	} else if !strings.Contains(string(out), "Usage: wtx run refresh") {
		t.Errorf("--help output = %q", out)
	}
	if err := exec.Command(dest, "--bogus").Run(); err == nil {
		t.Error("unknown argument should fail")
	}
}

func TestWriteStarterScriptsDoesNotOverwrite(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{SharedDir: config.DefaultSharedDir}
	dest := filepath.Join(root, "bin", "refresh")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("custom\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WriteStarterScripts(root, cfg, false); err != nil {
		t.Fatalf("WriteStarterScripts error: %v", err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "custom\n" {
		t.Errorf("existing script was overwritten: %q", got)
	}
}

func TestWriteStarterScriptsDryRun(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{SharedDir: config.DefaultSharedDir}
	if err := WriteStarterScripts(root, cfg, true); err != nil {
		t.Fatalf("dry-run error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "refresh")); err == nil {
		t.Error("dry-run wrote the starter script")
	}
}

// Exercise the commented resolution example users enable when implementing refresh.
func TestRefreshTemplateEnvironmentCompatibility(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	content, err := starterFS.ReadFile("templates/refresh.sh")
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	script.WriteString("set -euo pipefail\n")
	for _, line := range strings.Split(string(content), "\n") {
		if strings.HasPrefix(line, "# main=") || strings.HasPrefix(line, "# shared=") || strings.HasPrefix(line, "# : ") {
			script.WriteString(strings.TrimPrefix(line, "# ") + "\n")
		}
	}
	script.WriteString(`printf '%s\n%s\n' "$main" "$shared"`)
	tests := []struct {
		name string
		env  []string
		want string
		fail bool
	}{
		{"new prefix", []string{"WTX_MAIN_WORKTREE_PATH=/new main", "WTX_SHARED_PATH=/new shared"}, "/new main\n/new shared\n", false},
		{"legacy prefix", []string{"WT_MAIN_WORKTREE_PATH=/old main", "WT_SHARED_PATH=/old shared"}, "/old main\n/old shared\n", false},
		{"new prefix wins", []string{"WTX_MAIN_WORKTREE_PATH=/new", "WTX_SHARED_PATH=/new/shared", "WT_MAIN_WORKTREE_PATH=/old", "WT_SHARED_PATH=/old/shared"}, "/new\n/new/shared\n", false},
		{"empty new prefix falls back", []string{"WTX_MAIN_WORKTREE_PATH=", "WTX_SHARED_PATH=", "WT_MAIN_WORKTREE_PATH=/old", "WT_SHARED_PATH=/old/shared"}, "/old\n/old/shared\n", false},
		{"missing main", nil, "run: wtx add main", true},
		{"new branch hint wins", []string{"WTX_MAIN_BRANCH=trunk", "WT_MAIN_BRANCH=master"}, "run: wtx add trunk", true},
		{"legacy branch hint", []string{"WT_MAIN_BRANCH=master"}, "run: wtx add master", true},
		{"missing shared", []string{"WTX_MAIN_WORKTREE_PATH=/main"}, "run this via: wtx run refresh", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", script.String())
			cmd.Env = tt.env
			out, err := cmd.CombinedOutput()
			if (err != nil) != tt.fail {
				t.Fatalf("exit error = %v, want failure %v; output: %s", err, tt.fail, out)
			}
			if tt.fail {
				if !strings.Contains(string(out), tt.want) {
					t.Errorf("output = %q, want hint %q", out, tt.want)
				}
			} else if string(out) != tt.want {
				t.Errorf("output = %q, want %q", out, tt.want)
			}
		})
	}
}
