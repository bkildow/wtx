package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
)

// envAt is a ProjectEnv for a worktree at path with nothing else set.
func envAt(path string) ProjectEnv {
	return ProjectEnv{Vars: TemplateVars{WorktreePath: path}}
}

func TestRunSetupHooks(t *testing.T) {
	cfg := &config.Config{
		Setup: []string{"echo hello"},
	}
	wt := t.TempDir()

	err := RunSetupHooks(context.Background(), cfg, envAt(wt), false, nil)
	if err != nil {
		t.Fatalf("RunSetupHooks error: %v", err)
	}
}

func TestRunSetupHooksDryRun(t *testing.T) {
	cfg := &config.Config{
		Setup: []string{"echo hello"},
	}
	wt := t.TempDir()

	err := RunSetupHooks(context.Background(), cfg, envAt(wt), true, nil)
	if err != nil {
		t.Fatalf("RunSetupHooks dry-run error: %v", err)
	}
}

func TestRunSetupHooksFailure(t *testing.T) {
	cfg := &config.Config{
		Setup: []string{"false"},
	}
	wt := t.TempDir()

	err := RunSetupHooks(context.Background(), cfg, envAt(wt), false, nil)
	if err == nil {
		t.Fatal("expected error from failing hook")
	}
}

func TestRunSetupHooksEmpty(t *testing.T) {
	cfg := &config.Config{}
	wt := t.TempDir()

	err := RunSetupHooks(context.Background(), cfg, envAt(wt), false, nil)
	if err != nil {
		t.Fatalf("RunSetupHooks with empty hooks error: %v", err)
	}
}

func TestRunSetupHooksContinuesOnFailure(t *testing.T) {
	cfg := &config.Config{
		Setup: []string{"echo ok", "false", "echo still-runs"},
	}
	wt := t.TempDir()

	err := RunSetupHooks(context.Background(), cfg, envAt(wt), false, nil)
	if err == nil {
		t.Fatal("expected error from failing hook")
	}
}

func TestRunTeardownHooks(t *testing.T) {
	cfg := &config.Config{
		Teardown: []string{"echo cleanup"},
	}
	wt := t.TempDir()

	err := RunTeardownHooks(context.Background(), cfg, envAt(wt), false)
	if err != nil {
		t.Fatalf("RunTeardownHooks error: %v", err)
	}
}

func TestRunTeardownHooksEmpty(t *testing.T) {
	cfg := &config.Config{}
	wt := t.TempDir()

	err := RunTeardownHooks(context.Background(), cfg, envAt(wt), false)
	if err != nil {
		t.Fatalf("RunTeardownHooks with empty hooks error: %v", err)
	}
}

func TestRunTeardownHooksFailure(t *testing.T) {
	cfg := &config.Config{
		Teardown: []string{"false"},
	}
	wt := t.TempDir()

	err := RunTeardownHooks(context.Background(), cfg, envAt(wt), false)
	if err == nil {
		t.Fatal("expected error from failing teardown hook")
	}
}

func TestRunParallelSetupHooks(t *testing.T) {
	wt := t.TempDir()
	cfg := &config.Config{
		ParallelSetup: []string{
			"echo hello",
			"echo world",
		},
	}

	err := RunParallelSetupHooks(context.Background(), cfg, envAt(wt), false)
	if err != nil {
		t.Fatalf("RunParallelSetupHooks error: %v", err)
	}
}

func TestRunParallelSetupHooksDryRun(t *testing.T) {
	wt := t.TempDir()
	cfg := &config.Config{
		ParallelSetup: []string{"echo hello", "echo world"},
	}

	err := RunParallelSetupHooks(context.Background(), cfg, envAt(wt), true)
	if err != nil {
		t.Fatalf("RunParallelSetupHooks dry-run error: %v", err)
	}
}

func TestRunParallelSetupHooksEmpty(t *testing.T) {
	wt := t.TempDir()
	cfg := &config.Config{}

	err := RunParallelSetupHooks(context.Background(), cfg, envAt(wt), false)
	if err != nil {
		t.Fatalf("RunParallelSetupHooks with empty hooks error: %v", err)
	}
}

func TestRunParallelSetupHooksFailure(t *testing.T) {
	wt := t.TempDir()
	cfg := &config.Config{
		ParallelSetup: []string{"echo ok", "false", "echo still-runs"},
	}

	err := RunParallelSetupHooks(context.Background(), cfg, envAt(wt), false)
	if err == nil {
		t.Fatal("expected error from failing parallel setup hook")
	}
}

func TestRunParallelSetupHooksConcurrency(t *testing.T) {
	wt := t.TempDir()
	// Each command writes a file; verify all files exist afterward.
	cfg := &config.Config{
		ParallelSetup: []string{
			"touch " + filepath.Join(wt, "a.txt"),
			"touch " + filepath.Join(wt, "b.txt"),
			"touch " + filepath.Join(wt, "c.txt"),
		},
	}

	err := RunParallelSetupHooks(context.Background(), cfg, envAt(wt), false)
	if err != nil {
		t.Fatalf("RunParallelSetupHooks error: %v", err)
	}

	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if _, err := os.Stat(filepath.Join(wt, name)); err != nil {
			t.Errorf("expected file %s to exist: %v", name, err)
		}
	}
}

func TestRunParallelTeardownHooks(t *testing.T) {
	wt := t.TempDir()
	cfg := &config.Config{
		ParallelTeardown: []string{"echo cleanup1", "echo cleanup2"},
	}

	err := RunParallelTeardownHooks(context.Background(), cfg, envAt(wt), false)
	if err != nil {
		t.Fatalf("RunParallelTeardownHooks error: %v", err)
	}
}

func TestRunParallelTeardownHooksFailure(t *testing.T) {
	wt := t.TempDir()
	cfg := &config.Config{
		ParallelTeardown: []string{"echo ok", "false"},
	}

	err := RunParallelTeardownHooks(context.Background(), cfg, envAt(wt), false)
	if err == nil {
		t.Fatal("expected error from failing parallel teardown hook")
	}
}

func TestHooksExportProjectEnv(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, "worktrees", "feature", "X")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	env := ProjectEnv{
		Vars:             NewTemplateVars(root, wt, "feature/X"),
		SharedPath:       filepath.Join(root, "shared"),
		MainBranch:       "main",
		MainWorktreePath: root,
	}
	probe := func(out string) string {
		return `printf '%s\n' "$WTX_PROJECT_ROOT" "$WTX_SHARED_PATH" "$WTX_MAIN_BRANCH" "$WTX_MAIN_WORKTREE_PATH" "$WTX_WORKTREE_PATH" "$WTX_WORKTREE_ID" "$WTX_BRANCH_NAME" "${WT_WORKTREE_ID-unset}" > ` + out
	}
	cfg := &config.Config{
		Setup:            []string{probe("setup.txt")},
		ParallelSetup:    []string{probe("parallel-setup.txt")},
		Teardown:         []string{probe("teardown.txt")},
		ParallelTeardown: []string{probe("parallel-teardown.txt")},
	}

	ctx := context.Background()
	if err := RunSetupHooks(ctx, cfg, env, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := RunParallelSetupHooks(ctx, cfg, env, false); err != nil {
		t.Fatal(err)
	}
	if err := RunTeardownHooks(ctx, cfg, env, false); err != nil {
		t.Fatal(err)
	}
	if err := RunParallelTeardownHooks(ctx, cfg, env, false); err != nil {
		t.Fatal(err)
	}

	want := strings.Join([]string{
		filepath.Clean(root), filepath.Join(root, "shared"), "main", root, wt, "feature-x", "feature/X", "unset",
	}, "\n") + "\n"
	for _, out := range []string{"setup.txt", "parallel-setup.txt", "teardown.txt", "parallel-teardown.txt"} {
		// Each hook writes relative to its working directory, the worktree.
		data, err := os.ReadFile(filepath.Join(wt, out))
		if err != nil {
			t.Fatalf("%s: %v", out, err)
		}
		if string(data) != want {
			t.Errorf("%s = %q, want %q", out, data, want)
		}
	}
}
