package cmd

import (
	"fmt"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:               "apply [name]",
		Short:             "Apply shared files to a worktree",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktreeNames,
		RunE:              runApply,
	}
	cmd.Flags().Bool("all", false, "Apply to all worktrees")
	return cmd
}

func runApply(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	dry := IsDryRun()

	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	runner := git.NewRunner(project.GitDirPath(projectRoot, cfg), dry)

	worktrees, err := runner.WorktreeList(ctx)
	if err != nil {
		return err
	}

	filtered := filterManagedWorktrees(worktrees, projectRoot)

	if len(filtered) == 0 {
		return fmt.Errorf("no worktrees found")
	}

	all, _ := cmd.Flags().GetBool("all")

	if all {
		var totalResult project.ApplyResult
		for _, wt := range filtered {
			ui.Step("Applying to: " + wt.Branch)
			vars := project.NewTemplateVars(projectRoot, wt.Path, wt.Branch)
			result, err := project.Apply(projectRoot, wt.Path, cfg, dry, &vars)
			if err != nil {
				return err
			}
			totalResult.Copied += result.Copied
			totalResult.Symlinked += result.Symlinked
		}
		ui.Success(fmt.Sprintf("Applied shared files to %d worktree(s) (%d copied, %d symlinked)",
			len(filtered), totalResult.Copied, totalResult.Symlinked))
		return nil
	}

	selected, err := selectWorktree(args, filtered)
	if err != nil {
		if ui.IsUserAbort(err) {
			return nil
		}
		return err
	}

	vars := project.NewTemplateVars(projectRoot, selected.Path, selected.Branch)
	result, err := project.Apply(projectRoot, selected.Path, cfg, dry, &vars)
	if err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Applied shared files to: %s (%d copied, %d symlinked)",
		selected.Branch, result.Copied, result.Symlinked))
	return nil
}
