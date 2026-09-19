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
	for _, want := range []string{"#!/usr/bin/env bash", "WT_MAIN_WORKTREE_PATH", "WT_SHARED_PATH", "NOTE TO AI AGENTS", "docker compose"} {
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
	if err := exec.Command(dest, "--help").Run(); err != nil {
		t.Errorf("--help failed: %v", err)
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
