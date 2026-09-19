package project

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/ui"
)

// prefixWriter wraps an io.Writer and prepends a prefix to each line.
type prefixWriter struct {
	prefix string
	mu     *sync.Mutex
	buf    bytes.Buffer
}

func (pw *prefixWriter) Write(p []byte) (int, error) {
	pw.buf.Write(p)
	for {
		line, err := pw.buf.ReadBytes('\n')
		if err != nil {
			// incomplete line — put it back for next write
			pw.buf.Write(line)
			break
		}
		pw.mu.Lock()
		fmt.Fprintf(ui.Output, "[%s] %s", pw.prefix, line)
		pw.mu.Unlock()
	}
	return len(p), nil
}

// flush writes any remaining partial line.
func (pw *prefixWriter) flush() {
	if pw.buf.Len() > 0 {
		pw.mu.Lock()
		fmt.Fprintf(ui.Output, "[%s] %s\n", pw.prefix, pw.buf.String())
		pw.mu.Unlock()
		pw.buf.Reset()
	}
}

// HookProgressFunc is called after each serial hook completes.
// index is the 0-based position, cmdStr is the command, err is nil on success.
type HookProgressFunc func(index int, cmdStr string, err error)

// RunSetupHooks executes each command in cfg.Setup inside the
// worktree directory. Failures are logged but do not stop subsequent hooks.
// An optional onProgress callback is called after each hook completes.
func RunSetupHooks(ctx context.Context, cfg *config.Config, worktreePath string, dryRun bool, onProgress HookProgressFunc) error {
	if len(cfg.Setup) == 0 {
		return nil
	}

	failCount := 0
	for i, cmdStr := range cfg.Setup {
		ui.Step("Running: " + cmdStr)

		if dryRun {
			ui.DryRunNotice("exec: " + cmdStr)
			if onProgress != nil {
				onProgress(i, cmdStr, nil)
			}
			continue
		}

		cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
		cmd.Dir = worktreePath
		cmd.Stdin = os.Stdin
		cmd.Stdout = ui.Output
		cmd.Stderr = ui.Output

		var hookErr error
		if err := cmd.Run(); err != nil {
			ui.Error("Failed: " + cmdStr + ": " + err.Error())
			hookErr = err
			failCount++
		} else {
			ui.Success("Completed: " + cmdStr)
		}

		if onProgress != nil {
			onProgress(i, cmdStr, hookErr)
		}
	}

	if failCount > 0 {
		return fmt.Errorf("%d setup hook(s) failed", failCount)
	}
	return nil
}

// RunTeardownHooks executes each command in cfg.Teardown inside the
// worktree directory. Failures are logged but do not stop subsequent hooks.
func RunTeardownHooks(ctx context.Context, cfg *config.Config, worktreePath string, dryRun bool) error {
	if len(cfg.Teardown) == 0 {
		return nil
	}

	failCount := 0
	for _, cmdStr := range cfg.Teardown {
		ui.Step("Running: " + cmdStr)

		if dryRun {
			ui.DryRunNotice("exec: " + cmdStr)
			continue
		}

		cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
		cmd.Dir = worktreePath
		cmd.Stdin = os.Stdin
		cmd.Stdout = ui.Output
		cmd.Stderr = ui.Output

		if err := cmd.Run(); err != nil {
			ui.Error("Failed: " + cmdStr + ": " + err.Error())
			failCount++
			continue
		}
		ui.Success("Completed: " + cmdStr)
	}

	if failCount > 0 {
		return fmt.Errorf("%d teardown hook(s) failed", failCount)
	}
	return nil
}

// RunParallelSetupHooks executes all commands in cfg.ParallelSetup concurrently
// inside the worktree directory. All commands run to completion even if some fail.
func RunParallelSetupHooks(ctx context.Context, cfg *config.Config, worktreePath string, dryRun bool) error {
	return runParallelHooks(ctx, cfg.ParallelSetup, worktreePath, dryRun, "parallel setup")
}

// RunParallelTeardownHooks executes all commands in cfg.ParallelTeardown concurrently
// inside the worktree directory. All commands run to completion even if some fail.
func RunParallelTeardownHooks(ctx context.Context, cfg *config.Config, worktreePath string, dryRun bool) error {
	return runParallelHooks(ctx, cfg.ParallelTeardown, worktreePath, dryRun, "parallel teardown")
}

func runParallelHooks(ctx context.Context, hooks []string, worktreePath string, dryRun bool, label string) error {
	if len(hooks) == 0 {
		return nil
	}

	if dryRun {
		for _, cmdStr := range hooks {
			ui.DryRunNotice("exec (" + label + "): " + cmdStr)
		}
		return nil
	}

	ui.Step(fmt.Sprintf("Running %d %s hook(s) in parallel", len(hooks), label))

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		failCount int
		outputMu  sync.Mutex
	)

	for _, cmdStr := range hooks {
		wg.Add(1)
		go func(cmdStr string) {
			defer wg.Done()

			pw := &prefixWriter{prefix: cmdStr, mu: &outputMu}
			cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
			cmd.Dir = worktreePath
			cmd.Stdin = os.Stdin
			cmd.Stdout = pw
			cmd.Stderr = pw

			if err := cmd.Run(); err != nil {
				pw.flush()
				mu.Lock()
				failCount++
				mu.Unlock()
				outputMu.Lock()
				fmt.Fprintf(ui.Output, "[%s] Failed: %s\n", cmdStr, err.Error())
				outputMu.Unlock()
				return
			}
			pw.flush()
			outputMu.Lock()
			fmt.Fprintf(ui.Output, "[%s] Completed\n", cmdStr)
			outputMu.Unlock()
		}(cmdStr)
	}

	wg.Wait()

	if failCount > 0 {
		return fmt.Errorf("%d %s hook(s) failed", failCount, label)
	}
	return nil
}
