package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newRepairCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "repair",
		Short: "Repair worktree git config for compatibility with git 2.52+",
		Long: "Enables extensions.worktreeConfig on the common dir and writes core.bare=false " +
			"into each linked worktree's config.worktree. Idempotent — safe to re-run.\n\n" +
			"Use this on existing projects after upgrading wt or git: git 2.52+ refuses " +
			"auto-discovery from a worktree attached to a bare common dir unless each " +
			"worktree explicitly overrides core.bare.",
		Args: cobra.NoArgs,
		RunE: runRepair,
	}
}

func runRepair(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dry := IsDryRun()

	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	gitDir := project.GitDirPath(projectRoot, cfg)
	runner := git.NewRunner(gitDir, dry)

	ui.Step("Enabling extensions.worktreeConfig on " + cfg.GitDir)
	if err := runner.EnableWorktreeConfig(ctx); err != nil {
		return err
	}

	worktrees, err := runner.WorktreeList(ctx)
	if err != nil {
		return err
	}

	inspected, repaired := 0, 0
	for _, wt := range worktrees {
		if wt.Bare {
			continue
		}
		inspected++
		ok, err := worktreeBareOverrideOK(wt.Path)
		if err != nil {
			ui.Warning(fmt.Sprintf("Could not inspect %s: %v", wt.Path, err))
			continue
		}
		if ok {
			continue
		}
		ui.Step("Repairing worktree: " + displayPath(projectRoot, wt.Path))
		if err := runner.SetWorktreeBareFalse(ctx, wt.Path); err != nil {
			return err
		}
		repaired++
	}

	ui.Success(fmt.Sprintf("Repair complete: %d worktree(s) inspected, %d repaired", inspected, repaired))
	return nil
}

// worktreeBareOverrideOK reports whether the worktree's config.worktree
// already contains core.bare = false. Returns true when the override is in
// place, false when missing or when the file doesn't exist.
func worktreeBareOverrideOK(worktreePath string) (bool, error) {
	cfgPath, err := worktreeConfigPath(worktreePath)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return hasBareFalse(string(data)), nil
}

// worktreeConfigPath resolves <worktreePath>/.git to its actual gitdir
// (worktrees have a .git file: "gitdir: <relative-or-absolute-path>") and
// returns the path to its config.worktree file.
func worktreeConfigPath(worktreePath string) (string, error) {
	gitFile := filepath.Join(worktreePath, ".git")
	info, err := os.Stat(gitFile)
	if err != nil {
		return "", err
	}
	// In init setups the main worktree's .git is a real directory.
	if info.IsDir() {
		return filepath.Join(gitFile, "config.worktree"), nil
	}
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return "", err
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if gitdir == "" {
		return "", fmt.Errorf("malformed .git file at %s", gitFile)
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(worktreePath, gitdir)
	}
	return filepath.Join(gitdir, "config.worktree"), nil
}

// hasBareFalse parses a git config blob and reports whether [core] bare = false
// is set. We don't need a full INI parser — a tolerant scan is enough for the
// repair check.
func hasBareFalse(s string) bool {
	inCore := false
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inCore = strings.EqualFold(strings.Trim(line, "[]"), "core")
			continue
		}
		if !inCore {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(k), "bare") {
			continue
		}
		return strings.EqualFold(strings.TrimSpace(v), "false")
	}
	return false
}

func displayPath(projectRoot, p string) string {
	if rel, err := filepath.Rel(projectRoot, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}
