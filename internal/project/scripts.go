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

// ErrNoScripts is returned when neither .worktree.yml nor the bin directory
// provides any script.
var ErrNoScripts = errors.New("no scripts configured in .worktree.yml or found in the bin directory")

// ScriptNames returns the script names configured in .worktree.yml in
// sorted order.
func ScriptNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Scripts))
	for name := range cfg.Scripts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// AvailableScriptNames returns every name `wtx run` accepts, sorted: the
// configured scripts plus the executables in the bin directory (see
// BinScriptNames).
func AvailableScriptNames(projectRoot string, cfg *config.Config) []string {
	names := ScriptNames(cfg)
	for _, name := range BinScriptNames(projectRoot, cfg) {
		if _, ok := cfg.Scripts[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// BinScriptNames returns the names of the executable files directly in the
// bin directory (BinPath), skipping hidden files. They run by name without
// a scripts entry.
func BinScriptNames(projectRoot string, cfg *config.Config) []string {
	entries, err := os.ReadDir(BinPath(projectRoot, cfg))
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if _, ok := binScript(projectRoot, cfg, entry.Name()); ok {
			names = append(names, entry.Name())
		}
	}
	return names
}

// binScript returns the bin directory file for name when it is a visible,
// executable regular file (symlinks are followed).
func binScript(projectRoot string, cfg *config.Config, name string) (string, bool) {
	if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, `/\`) {
		return "", false
	}
	path := filepath.Join(BinPath(projectRoot, cfg), name)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return "", false
	}
	return path, true
}

// ResolveScript looks up a named script and returns its absolute path. A
// scripts entry in cfg wins; its path is expanded with ExpandPath ("~" and
// "~/.wtx" forms, absolute as-is, relative against projectRoot) and must be
// an existing executable file. Otherwise an executable file of that name in
// the bin directory (BinPath) is used.
func ResolveScript(cfg *config.Config, projectRoot, name string) (string, error) {
	rel, ok := cfg.Scripts[name]
	if !ok {
		if path, ok := binScript(projectRoot, cfg, name); ok {
			return path, nil
		}
		names := AvailableScriptNames(projectRoot, cfg)
		if len(names) == 0 {
			return "", ErrNoScripts
		}
		return "", fmt.Errorf("unknown script %q (available: %s)", name, strings.Join(names, ", "))
	}
	if rel == "" {
		return "", fmt.Errorf("script %q has an empty path in .worktree.yml", name)
	}

	path, err := ExpandPath(projectRoot, rel)
	if err != nil {
		return "", fmt.Errorf("script %q: %w", name, err)
	}

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

// ProjectEnv is what a hook or script learns about the Project and the
// worktree it runs for, exported as WTX_* environment variables.
type ProjectEnv struct {
	Vars             TemplateVars // the worktree's template variables
	SharedPath       string       // absolute shared directory
	MainBranch       string       // configured main branch
	MainWorktreePath string       // absolute path of the Main worktree, or "" if not checked out
}

// environ returns the variables as NAME=value, without a prefix.
func (e ProjectEnv) environ() []string {
	return []string{
		"PROJECT_ROOT=" + e.Vars.ProjectRoot,
		"SHARED_PATH=" + e.SharedPath,
		"MAIN_BRANCH=" + e.MainBranch,
		"MAIN_WORKTREE_PATH=" + e.MainWorktreePath,
		"WORKTREE_PATH=" + e.Vars.WorktreePath,
		"WORKTREE_ID=" + e.Vars.WorktreeID,
		"BRANCH_NAME=" + e.Vars.BranchName,
	}
}

// ScriptRun describes a single script invocation.
type ScriptRun struct {
	Name       string   // configured script name
	Path       string   // absolute path to the executable
	Args       []string // pass-through arguments
	Dir        string   // working directory for the script
	ProjectEnv          // exported as WTX_* and WT_* environment variables
}

// ScriptEnv returns the WTX_* and legacy WT_* variables exported to a script.
func ScriptEnv(run ScriptRun) []string {
	values := append([]string{"SCRIPT_NAME=" + run.Name}, run.environ()...)
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
