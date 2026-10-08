package cmd

import (
	"fmt"

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

	clone, err := openClone(ctx)
	if err != nil {
		return err
	}
	projectRoot, cfg := clone.Root(), clone.Config()

	filtered, err := clone.ManagedWorktrees(ctx)
	if err != nil {
		return err
	}

	if len(filtered) == 0 {
		return fmt.Errorf("no worktrees found")
	}

	all, _ := cmd.Flags().GetBool("all")

	if all {
		include := resolveIncludeSource(ctx, clone)
		var totalResult project.ApplyResult
		for _, wt := range filtered {
			ui.Step("Applying to: " + wt.Branch)
			vars := project.NewTemplateVars(projectRoot, wt.Path, wt.Branch)
			result, err := project.Apply(projectRoot, wt.Path, cfg, dry, &vars, include)
			if err != nil {
				return err
			}
			totalResult.Included += result.Included
			totalResult.Copied += result.Copied
			totalResult.Symlinked += result.Symlinked
		}
		ui.Success(fmt.Sprintf("Applied shared files to %d worktree(s) (%d included, %d copied, %d symlinked)",
			len(filtered), totalResult.Included, totalResult.Copied, totalResult.Symlinked))
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
	result, err := project.Apply(projectRoot, selected.Path, cfg, dry, &vars, resolveIncludeSource(ctx, clone))
	if err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Applied shared files to: %s (%d included, %d copied, %d symlinked)",
		selected.Branch, result.Included, result.Copied, result.Symlinked))
	return nil
}
