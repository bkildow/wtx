package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

var knownEditors = []struct{ Name, Binary string }{
	{"Cursor", "cursor"},
	{"VS Code", "code"},
	{"Zed", "zed"},
}

func newOpenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "open [name]",
		Short:             "Open a worktree in an IDE",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktreeNames,
		RunE:              runOpen,
	}
	return cmd
}

func runOpen(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	runner := git.NewRunner(project.GitDirPath(projectRoot, cfg), IsDryRun())
	worktrees, err := runner.WorktreeList(ctx)
	if err != nil {
		return err
	}

	filtered := filterManagedWorktrees(worktrees, projectRoot)

	if len(filtered) == 0 {
		return fmt.Errorf("no worktrees found")
	}

	selected, err := selectWorktree(args, filtered)
	if err != nil {
		if ui.IsUserAbort(err) {
			return nil
		}
		return err
	}

	// Determine editor: config > $EDITOR > auto-detect
	var editorBinary string

	// 1. Config file
	if cfg.Editor != "" {
		if _, err := exec.LookPath(cfg.Editor); err != nil {
			return fmt.Errorf("configured editor not found: %s", cfg.Editor)
		}
		editorBinary = cfg.Editor
	}

	// 2. $EDITOR environment variable
	if editorBinary == "" {
		if env := os.Getenv("EDITOR"); env != "" {
			if _, err := exec.LookPath(env); err != nil {
				return fmt.Errorf("$EDITOR not found: %s", env)
			}
			editorBinary = env
		}
	}

	// 3. Auto-detect known editors
	if editorBinary == "" {
		var available []string
		for _, e := range knownEditors {
			if _, err := exec.LookPath(e.Binary); err == nil {
				available = append(available, e.Binary)
			}
		}
		switch len(available) {
		case 0:
			return fmt.Errorf("no editor found: set 'editor' in .worktree.yml or $EDITOR")
		case 1:
			editorBinary = available[0]
		default:
			prompter := &ui.InteractivePrompter{}
			editorBinary, err = prompter.SelectEditor(available)
			if err != nil {
				if ui.IsUserAbort(err) {
					return nil
				}
				return err
			}
		}
	}

	if IsDryRun() {
		ui.DryRunNotice(fmt.Sprintf("%s %s", editorBinary, selected.Path))
		return nil
	}

	ui.Step(fmt.Sprintf("Opening %s in %s", selected.Branch, editorBinary))
	c := exec.Command(editorBinary, selected.Path)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}
