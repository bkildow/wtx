package project

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkildow/wtx/internal/config"
)

func TestRunSetupHooks(t *testing.T) {
	cfg := &config.Config{
		Setup: []string{"echo hello"},
	}
	wt := t.TempDir()

	err := RunSetupHooks(context.Background(), cfg, wt, false, nil)
	if err != nil {
		t.Fatalf("RunSetupHooks error: %v", err)
	}
}

func TestRunSetupHooksDryRun(t *testing.T) {
	cfg := &config.Config{
		Setup: []string{"echo hello"},
	}
	wt := t.TempDir()

	err := RunSetupHooks(context.Background(), cfg, wt, true, nil)
	if err != nil {
		t.Fatalf("RunSetupHooks dry-run error: %v", err)
	}
}

func TestRunSetupHooksFailure(t *testing.T) {
	cfg := &config.Config{
		Setup: []string{"false"},
	}
	wt := t.TempDir()

	err := RunSetupHooks(context.Background(), cfg, wt, false, nil)
	if err == nil {
		t.Fatal("expected error from failing hook")
	}
}

func TestRunSetupHooksEmpty(t *testing.T) {
	cfg := &config.Config{}
	wt := t.TempDir()

	err := RunSetupHooks(context.Background(), cfg, wt, false, nil)
	if err != nil {
		t.Fatalf("RunSetupHooks with empty hooks error: %v", err)
	}
}

func TestRunSetupHooksContinuesOnFailure(t *testing.T) {
	cfg := &config.Config{
		Setup: []string{"echo ok", "false", "echo still-runs"},
	}
	wt := t.TempDir()

	err := RunSetupHooks(context.Background(), cfg, wt, false, nil)
	if err == nil {
		t.Fatal("expected error from failing hook")
	}
}

func TestRunTeardownHooks(t *testing.T) {
	cfg := &config.Config{
		Teardown: []string{"echo cleanup"},
	}
	wt := t.TempDir()

	err := RunTeardownHooks(context.Background(), cfg, wt, false)
	if err != nil {
		t.Fatalf("RunTeardownHooks error: %v", err)
	}
}

func TestRunTeardownHooksEmpty(t *testing.T) {
	cfg := &config.Config{}
	wt := t.TempDir()

	err := RunTeardownHooks(context.Background(), cfg, wt, false)
	if err != nil {
		t.Fatalf("RunTeardownHooks with empty hooks error: %v", err)
	}
}

func TestRunTeardownHooksFailure(t *testing.T) {
	cfg := &config.Config{
		Teardown: []string{"false"},
	}
	wt := t.TempDir()

	err := RunTeardownHooks(context.Background(), cfg, wt, false)
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

	err := RunParallelSetupHooks(context.Background(), cfg, wt, false)
	if err != nil {
		t.Fatalf("RunParallelSetupHooks error: %v", err)
	}
}

func TestRunParallelSetupHooksDryRun(t *testing.T) {
	wt := t.TempDir()
	cfg := &config.Config{
		ParallelSetup: []string{"echo hello", "echo world"},
	}

	err := RunParallelSetupHooks(context.Background(), cfg, wt, true)
	if err != nil {
		t.Fatalf("RunParallelSetupHooks dry-run error: %v", err)
	}
}

func TestRunParallelSetupHooksEmpty(t *testing.T) {
	wt := t.TempDir()
	cfg := &config.Config{}

	err := RunParallelSetupHooks(context.Background(), cfg, wt, false)
	if err != nil {
		t.Fatalf("RunParallelSetupHooks with empty hooks error: %v", err)
	}
}

func TestRunParallelSetupHooksFailure(t *testing.T) {
	wt := t.TempDir()
	cfg := &config.Config{
		ParallelSetup: []string{"echo ok", "false", "echo still-runs"},
	}

	err := RunParallelSetupHooks(context.Background(), cfg, wt, false)
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

	err := RunParallelSetupHooks(context.Background(), cfg, wt, false)
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

	err := RunParallelTeardownHooks(context.Background(), cfg, wt, false)
	if err != nil {
		t.Fatalf("RunParallelTeardownHooks error: %v", err)
	}
}

func TestRunParallelTeardownHooksFailure(t *testing.T) {
	wt := t.TempDir()
	cfg := &config.Config{
		ParallelTeardown: []string{"echo ok", "false"},
	}

	err := RunParallelTeardownHooks(context.Background(), cfg, wt, false)
	if err == nil {
		t.Fatal("expected error from failing parallel teardown hook")
	}
}
