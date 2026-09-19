package project

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/ui"
)

// ErrNoScripts is returned when .worktree.yml has no scripts configured.
var ErrNoScripts = errors.New("no scripts configured in .worktree.yml")

// ScriptNames returns the configured script names in sorted order.
func ScriptNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Scripts))
	for name := range cfg.Scripts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ResolveScript looks up a named script in cfg and returns its absolute path.
// Relative paths are resolved against projectRoot. The file must exist and
// be executable.
func ResolveScript(cfg *config.Config, projectRoot, name string) (string, error) {
	if len(cfg.Scripts) == 0 {
		return "", ErrNoScripts
	}

	rel, ok := cfg.Scripts[name]
	if !ok {
		return "", fmt.Errorf("unknown script %q (available: %s)", name, strings.Join(ScriptNames(cfg), ", "))
	}
	if rel == "" {
		return "", fmt.Errorf("script %q has an empty path in .worktree.yml", name)
	}

	path := rel
	if !filepath.IsAbs(path) {
		path = filepath.Join(projectRoot, path)
	}
	path = filepath.Clean(path)

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("script %q not found: %s", name, path)
		}
		return "", fmt.Errorf("script %q: %w", name, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("script %q is a directory: %s", name, path)
	}
	if info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("script %q is not executable: %s (try: chmod +x %s)", name, path, path)
	}

	return path, nil
}

// ScriptRun describes a single script invocation.
type ScriptRun struct {
	Name string       // configured script name
	Path string       // absolute path to the executable
	Args []string     // pass-through arguments
	Dir  string       // working directory for the script
	Vars TemplateVars // exported as WTX_* and WT_* environment variables

	SharedPath       string // absolute shared directory
	MainBranch       string // configured main branch
	MainWorktreePath string // absolute path of the main branch's worktree, or "" if not checked out
}

// ScriptEnv returns the WTX_* and legacy WT_* variables exported to a script.
func ScriptEnv(run ScriptRun) []string {
	values := []string{
		"SCRIPT_NAME=" + run.Name,
		"PROJECT_ROOT=" + run.Vars.ProjectRoot,
		"SHARED_PATH=" + run.SharedPath,
		"MAIN_BRANCH=" + run.MainBranch,
		"MAIN_WORKTREE_PATH=" + run.MainWorktreePath,
		"WORKTREE_PATH=" + run.Vars.WorktreePath,
		"WORKTREE_ID=" + run.Vars.WorktreeID,
		"BRANCH_NAME=" + run.Vars.BranchName,
	}
	env := make([]string, 0, 2*len(values))
	for _, value := range values {
		env = append(env, "WTX_"+value, "WT_"+value)
	}
	return env
}

// RunScript executes the script with stdin/stdout/stderr attached. The
// script's stdout goes to the process stdout (not ui.Output) so that
// `wt run export > file` and `$(wt run ...)` capture script output while wt's
// own messages stay on stderr. The process inherits the parent environment
// plus the WTX_* and WT_* variables. A non-zero exit is returned as an error carrying
// the exit code.
func RunScript(ctx context.Context, run ScriptRun, dryRun bool) error {
	return runScript(ctx, run, dryRun, os.Stdout, os.Stderr)
}

func runScript(ctx context.Context, run ScriptRun, dryRun bool, stdout, stderr io.Writer) error {
	display := run.Path
	if len(run.Args) > 0 {
		display += " " + strings.Join(run.Args, " ")
	}

	if dryRun {
		ui.DryRunNotice("exec: " + display)
		ui.DryRunNotice("  in: " + run.Dir)
		return nil
	}

	cmd := exec.CommandContext(ctx, run.Path, run.Args...)
	cmd.Dir = run.Dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = append(os.Environ(), ScriptEnv(run)...)

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("script %q exited with code %d", run.Name, exitErr.ExitCode())
		}
		return fmt.Errorf("script %q: %w", run.Name, err)
	}
	return nil
}
