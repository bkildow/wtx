package project

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/ui"
)

func writeScript(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), mode); err != nil {
		t.Fatal(err)
	}
}

func TestResolveScript(t *testing.T) {
	root := t.TempDir()
	writeScript(t, filepath.Join(root, "bin", "ok"), "exit 0\n", 0o755)
	writeScript(t, filepath.Join(root, "bin", "noexec"), "exit 0\n", 0o644)
	if err := os.MkdirAll(filepath.Join(root, "bin", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	absScript := filepath.Join(t.TempDir(), "abs")
	writeScript(t, absScript, "exit 0\n", 0o755)

	cfg := &config.Config{Scripts: map[string]string{
		"ok":      "bin/ok",
		"noexec":  "bin/noexec",
		"missing": "bin/missing",
		"dir":     "bin/dir",
		"abs":     absScript,
		"empty":   "",
	}}

	tests := []struct {
		name    string
		script  string
		want    string
		wantErr string
	}{
		{"relative", "ok", filepath.Join(root, "bin", "ok"), ""},
		{"absolute", "abs", absScript, ""},
		{"unknown", "nope", "", "unknown script \"nope\" (available: abs, dir, empty, missing, noexec, ok)"},
		{"missing", "missing", "", "not found"},
		{"not executable", "noexec", "", "not executable"},
		{"directory", "dir", "", "is a directory"},
		{"empty path", "empty", "", "empty path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveScript(cfg, root, tt.script)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("path = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveScriptNoScripts(t *testing.T) {
	_, err := ResolveScript(&config.Config{}, t.TempDir(), "x")
	if !errors.Is(err, ErrNoScripts) {
		t.Fatalf("err = %v, want ErrNoScripts", err)
	}
}

func TestScriptNamesSorted(t *testing.T) {
	cfg := &config.Config{Scripts: map[string]string{"zeta": "z", "alpha": "a", "mid": "m"}}
	got := ScriptNames(cfg)
	want := []string{"alpha", "mid", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("names = %v, want %v", got, want)
	}
}

func TestRunScriptArgsEnvAndCwd(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, "worktrees", "feature-x")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "bin", "probe")
	writeScript(t, script, `printf '%s\n' "$(pwd)" "$WT_SCRIPT_NAME" "$WT_PROJECT_ROOT" "$WT_WORKTREE_PATH" "$WT_WORKTREE_ID" "$WT_BRANCH_NAME" "$WT_SHARED_PATH" "$WT_MAIN_BRANCH" "$WT_MAIN_WORKTREE_PATH" "$#" "$1" "$2" > out.txt
`, 0o755)

	mainWT := filepath.Join(root, "worktrees", "main")
	err := RunScript(context.Background(), ScriptRun{
		Name:             "probe",
		Path:             script,
		Args:             []string{"--flag", "value with space"},
		Dir:              wt,
		Vars:             NewTemplateVars(root, wt, "feature/x"),
		SharedPath:       filepath.Join(root, "shared"),
		MainBranch:       "main",
		MainWorktreePath: mainWT,
	}, false)
	if err != nil {
		t.Fatalf("RunScript error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wt, "out.txt"))
	if err != nil {
		t.Fatalf("script did not write in cwd: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	want := []string{wt, "probe", filepath.Clean(root), wt, "feature-x", "feature/x", filepath.Join(root, "shared"), "main", mainWT, "2", "--flag", "value with space"}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines %q, want %d", len(lines), lines, len(want))
	}
	for i := range want {
		// pwd may resolve symlinks (e.g. /private/tmp on macOS); compare resolved paths for line 0.
		if i == 0 {
			gotResolved, _ := filepath.EvalSymlinks(lines[0])
			wantResolved, _ := filepath.EvalSymlinks(want[0])
			if gotResolved != wantResolved {
				t.Errorf("cwd = %q, want %q", lines[0], want[0])
			}
			continue
		}
		if lines[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, lines[i], want[i])
		}
	}
}

func TestRunScriptExitCode(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "fail")
	writeScript(t, script, "exit 3\n", 0o755)

	err := RunScript(context.Background(), ScriptRun{Name: "fail", Path: script, Dir: root}, false)
	if err == nil || !strings.Contains(err.Error(), `script "fail" exited with code 3`) {
		t.Fatalf("err = %v, want exit code 3", err)
	}
}

func TestRunScriptDryRun(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "touch-marker")
	writeScript(t, script, "touch marker\n", 0o755)

	var buf bytes.Buffer
	old := ui.Output
	ui.Output = &buf
	t.Cleanup(func() { ui.Output = old })

	err := RunScript(context.Background(), ScriptRun{Name: "touch-marker", Path: script, Args: []string{"a"}, Dir: root}, true)
	if err != nil {
		t.Fatalf("dry run error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "marker")); err == nil {
		t.Error("dry run executed the script")
	}
	if !strings.Contains(buf.String(), script+" a") {
		t.Errorf("dry run output missing command: %q", buf.String())
	}
}

func TestRunScriptSeparatesStdoutAndStderr(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "talk")
	writeScript(t, script, "echo to-stdout\necho to-stderr >&2\n", 0o755)

	var stdout, stderr bytes.Buffer
	err := runScript(context.Background(), ScriptRun{Name: "talk", Path: script, Dir: root}, false, &stdout, &stderr)
	if err != nil {
		t.Fatalf("runScript error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "to-stdout" {
		t.Errorf("stdout = %q, want %q", got, "to-stdout")
	}
	if got := strings.TrimSpace(stderr.String()); got != "to-stderr" {
		t.Errorf("stderr = %q, want %q", got, "to-stderr")
	}
}
