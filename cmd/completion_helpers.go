package cmd

import (
	"context"
	"os"

	"github.com/bkildow/wtx/internal/project"
	"github.com/spf13/cobra"
)

// completeWorktreeNames completes the branches of the Managed worktrees, for
// commands that change a worktree.
func completeWorktreeNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeNames(cmd, args, false)
}

// completeNavigableNames also completes the Main worktree's branch, for
// commands that only go to a worktree (see project.Worktrees.Navigable).
func completeNavigableNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return completeNames(cmd, args, true)
}

func completeNames(cmd *cobra.Command, args []string, withMain bool) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	names, err := listWorktreeNames(cmd.Context(), withMain)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

// listWorktreeNames lists "." and the Managed worktrees' branches, and with
// withMain the Main worktree's too.
func listWorktreeNames(ctx context.Context, withMain bool) ([]string, error) {
	clone, err := completionClone(ctx)
	if err != nil {
		return nil, err
	}

	wts, err := clone.Worktrees(ctx)
	if err != nil {
		return nil, err
	}
	filtered := wts.Managed
	if withMain {
		filtered = wts.Navigable()
	}

	names := make([]string, 0, len(filtered)+1)
	names = append(names, ".")
	for _, wt := range filtered {
		names = append(names, wt.Branch)
	}

	return names, nil
}

// completeBranchNames completes flag values with the project's remote branches.
func completeBranchNames(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	ctx := cmd.Context()
	clone, err := completionClone(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	branches, err := clone.Runner().ListRemoteBranches(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return branches, cobra.ShellCompDirectiveNoFileComp
}

// completeRemoteBranchArg completes the first positional argument with the
// project's remote branches.
func completeRemoteBranchArg(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return completeBranchNames(cmd, args, toComplete)
}

// completionClone opens the Clone containing the working directory for shell
// completion functions. Unlike openClone it prints nothing.
func completionClone(ctx context.Context) (*project.Clone, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return project.Open(ctx, cwd, project.Options{})
}
