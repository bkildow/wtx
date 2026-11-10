package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply [name]",
		Short: "Apply shared files to a worktree",
		Long: `Apply shared files to an existing worktree.

Copies .worktreeinclude and shared/copy files the worktree is missing and
links shared/symlink entries. Files the worktree already has are kept, so a
populated vendor/ or node_modules/ is never reset to an older shared copy.
Name a path with --overwrite to replace it, or pass --force to replace every
shared/copy file.`,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeWorktreeNames,
		RunE:              runApply,
	}
	cmd.Flags().Bool("all", false, "Apply to all worktrees")
	cmd.Flags().Bool("force", false, "Replace every shared/copy file the worktree already has")
	cmd.Flags().StringArray("overwrite", nil, "Replace this shared/copy path (file or directory, relative to the worktree root) if the worktree has it; repeatable")
	cmd.MarkFlagsMutuallyExclusive("force", "overwrite")
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

	mode, err := applyCopyMode(cmd, filepath.Join(clone.SharedDir(), "copy"))
	if err != nil {
		return err
	}

	wts, err := clone.Worktrees(ctx)
	if err != nil {
		return err
	}
	if err := rootApplyError(clone.Layout(), wts, args); err != nil {
		return err
	}

	if len(wts.Managed) == 0 {
		return fmt.Errorf("no worktrees found")
	}

	all, _ := cmd.Flags().GetBool("all")

	if all {
		include := includeSource(ctx, clone, wts)
		var totalResult project.ApplyResult
		for _, wt := range wts.Managed {
			ui.Step("Applying to: " + wt.Branch)
			vars := project.NewTemplateVars(projectRoot, wt.Path, wt.Branch)
			result, err := project.Apply(projectRoot, wt.Path, cfg, dry, &vars, include, mode)
			if err != nil {
				return err
			}
			totalResult.Included += result.Included
			totalResult.Copied += result.Copied
			totalResult.Kept += result.Kept
			totalResult.Symlinked += result.Symlinked
		}
		reportApply(fmt.Sprintf("Applied shared files to %d worktree(s)", len(wts.Managed)), totalResult)
		return nil
	}

	selected, err := selectWorktree(args, wts.Managed)
	if err != nil {
		if ui.IsUserAbort(err) {
			return nil
		}
		return err
	}

	vars := project.NewTemplateVars(projectRoot, selected.Path, selected.Branch)
	result, err := project.Apply(projectRoot, selected.Path, cfg, dry, &vars, includeSource(ctx, clone, wts), mode)
	if err != nil {
		return err
	}

	reportApply("Applied shared files to: "+selected.Branch, result)
	return nil
}

// applyCopyMode reads --force and --overwrite: apply keeps the shared/copy
// files a worktree already has unless one of them says otherwise.
func applyCopyMode(cmd *cobra.Command, copyDir string) (project.CopyMode, error) {
	if force, _ := cmd.Flags().GetBool("force"); force {
		return project.ReplaceExisting, nil
	}
	paths, _ := cmd.Flags().GetStringArray("overwrite")
	replace, err := project.ReplacePaths(copyDir, paths)
	if err != nil {
		return project.CopyMode{}, fmt.Errorf("--overwrite %w", err)
	}
	return project.CopyMode{Seed: true, Replace: replace}, nil
}

// reportApply prints the summary line, and how to replace the files apply
// kept.
func reportApply(prefix string, r project.ApplyResult) {
	kept := ""
	if r.Kept > 0 {
		kept = fmt.Sprintf(", %d kept", r.Kept)
	}
	ui.Success(fmt.Sprintf("%s (%d included, %d copied%s, %d symlinked)", prefix, r.Included, r.Copied, kept, r.Symlinked))
	if r.Kept > 0 {
		ui.Info("Shared files the worktree already had were kept; replace them with --overwrite <path> or --force")
	}
}

// rootApplyError explains why apply refuses the Project root when args names
// the branch it has checked out in a Checkout or In-repo layout: the root is
// the .worktreeinclude source, not a Managed worktree.
func rootApplyError(layout project.LayoutKind, wts project.Worktrees, args []string) error {
	if layout == project.BareLayout || !wts.HasMain || len(args) == 0 || wts.Main.Branch == "" || args[0] != wts.Main.Branch {
		return nil
	}
	if _, ok := findWorktreeByBranch(wts.Managed, args[0]); ok {
		return nil
	}
	return fmt.Errorf("%s is checked out at the project root, the .worktreeinclude source; wtx apply only targets managed worktrees", args[0])
}
