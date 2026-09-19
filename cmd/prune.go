package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkildow/wtx/internal/forge"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newPruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove worktrees with fully merged branches",
		Long: `Remove worktrees whose branch has been merged into the default branch.

Detects regular, squash, and rebase merges, plus merged pull requests when
gh is available. Merged worktrees with uncommitted changes are listed as
dirty and kept unless --force is given. A confirmation prompt is shown
before anything is removed; pass --yes to skip it.`,
		Args: cobra.NoArgs,
		RunE: runPrune,
	}
	cmd.Flags().Bool("force", false, "Remove merged worktrees even if they have uncommitted changes")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	cmd.Flags().Bool("skip-teardown", false, "Skip running teardown hooks before removing worktrees")
	return cmd
}

// prunable pairs a worktree with why it was judged merged, so the removal loop
// and the listing can both explain themselves.
type prunable struct {
	worktree git.WorktreeInfo
	method   git.MergeMethod
	reason   string
	dirty    bool
}

// status describes the working tree for the candidate listing.
func (p prunable) status() string {
	if p.dirty {
		return "dirty"
	}
	return "clean"
}

// partitionPrunable splits candidates into those prune will remove and those
// it will keep. Only dirty worktrees are ever kept, and only without --force.
func partitionPrunable(candidates []prunable, force bool) (remove, keep []prunable) {
	for _, p := range candidates {
		if p.dirty && !force {
			keep = append(keep, p)
			continue
		}
		remove = append(remove, p)
	}
	return remove, keep
}

func mergeReason(method git.MergeMethod) string {
	switch method {
	case git.MergeRebase:
		return "merged (rebase)"
	case git.MergeSquash:
		return "merged (squash)"
	case git.MergeEquivalent:
		return "merged (patch-equivalent)"
	default:
		return "merged"
	}
}

// prMerged reports whether a pull request is trustworthy evidence that this
// worktree's branch is done. State alone is not enough: gh matches PRs by head
// branch *name*, so a reused branch name or commits pushed after the merge
// would otherwise get a live worktree deleted. The PR must have merged into the
// branch prune is comparing against, and its head must still be the commit the
// worktree is sitting on.
func prMerged(ctx context.Context, runner *git.Runner, pr *forge.PullRequest, wt git.WorktreeInfo, defaultBranch string) bool {
	if pr == nil || pr.State != forge.StateMerged {
		return false
	}

	if pr.BaseRef != "" && pr.BaseRef != defaultBranch {
		return false
	}

	if pr.HeadOID == "" {
		return false
	}

	head := wt.Head
	if head == "" {
		resolved, err := runner.Query(ctx, "rev-parse", wt.Branch)
		if err != nil {
			return false
		}
		head = resolved
	}

	return head == pr.HeadOID
}

func runPrune(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	projectRoot, cfg, err := loadProject()
	if err != nil {
		return err
	}

	force, _ := cmd.Flags().GetBool("force")
	yes, _ := cmd.Flags().GetBool("yes")
	skipTeardown, _ := cmd.Flags().GetBool("skip-teardown")

	cwd, _ := os.Getwd()
	runner := git.NewRunner(project.GitDirPath(projectRoot, cfg), IsDryRun())

	defaultBranch := cfg.MainBranchOrDefault()

	worktrees, err := runner.WorktreeList(ctx)
	if err != nil {
		return err
	}

	filtered := filterManagedWorktrees(worktrees, projectRoot)

	// Resolve current worktree path for comparison
	currentPath := resolvePathBest(cwd)

	// Optional PR awareness. A missing or unusable gh is the normal case, not
	// an error: detection just falls back to the git-native checks.
	var f forge.Forge
	if remoteURL, err := runner.RemoteURL(ctx, "origin"); err == nil {
		f = forge.Detect(ctx, remoteURL)
	}

	// A worktree's dirtiness decides whether it is removed, so an unanswerable
	// question is treated as dirty rather than risking uncommitted work.
	isDirty := func(wt git.WorktreeInfo) bool {
		dirty, err := runner.IsWorktreeDirty(ctx, wt.Path)
		if err != nil {
			ui.Warning(fmt.Sprintf("%s: could not check for uncommitted changes, assuming dirty: %s", wt.Branch, err))
			return true
		}
		return dirty
	}

	var pruneable []prunable
	for _, wt := range filtered {
		if wt.Branch == defaultBranch {
			continue
		}

		if resolvePathBest(wt.Path) == currentPath {
			continue
		}

		status, err := runner.BranchMergeStatus(ctx, wt.Branch, defaultBranch)
		if err != nil {
			ui.Warning(fmt.Sprintf("%s: could not check merge status: %s", wt.Branch, err))
			continue
		}

		if status.Merged {
			pruneable = append(pruneable, prunable{
				worktree: wt,
				method:   status.Method,
				reason:   mergeReason(status.Method),
				dirty:    isDirty(wt),
			})
			continue
		}

		if f == nil {
			continue
		}

		pr, err := f.PRForBranch(ctx, wt.Branch)
		if err != nil {
			// One failure usually means gh is unauthenticated or offline, so
			// stop asking rather than repeating it for every worktree — but say
			// so, since the remaining worktrees are now judged by git alone.
			ui.Warning(fmt.Sprintf(
				"%s: could not check pull request state: %s\n  Pull request detection disabled for the rest of this run.",
				wt.Branch, err,
			))
			f = nil
			continue
		}

		if prMerged(ctx, runner, pr, wt, defaultBranch) {
			pruneable = append(pruneable, prunable{
				worktree: wt,
				method:   git.MergeNone,
				reason:   fmt.Sprintf("merged (PR #%d)", pr.Number),
				dirty:    isDirty(wt),
			})
		}
	}

	if len(pruneable) == 0 {
		ui.Info("No merged worktrees to prune.")
		return nil
	}

	ui.Step("Merged worktrees:")
	t := ui.NewTable().Headers("BRANCH", "PATH", "MERGED", "STATUS")
	for _, p := range pruneable {
		relPath, err := filepath.Rel(projectRoot, p.worktree.Path)
		if err != nil {
			relPath = p.worktree.Path
		}
		t.Row(p.worktree.Branch, relPath, p.reason, p.status())
	}
	ui.PrintTable(t)

	toRemove, kept := partitionPrunable(pruneable, force)
	if len(kept) > 0 {
		ui.Warning(fmt.Sprintf(
			"%d worktree(s) have uncommitted changes and will be kept. Pass --force to remove them too.",
			len(kept),
		))
	}

	if len(toRemove) == 0 {
		ui.Info("Nothing to prune.")
		return nil
	}

	if !yes && !IsDryRun() {
		prompter := &ui.InteractivePrompter{}
		confirmed, err := prompter.Confirm(fmt.Sprintf("Remove %d merged worktree(s)?", len(toRemove)))
		if err != nil {
			if ui.IsUserAbort(err) {
				return nil
			}
			return err
		}
		if !confirmed {
			ui.Info("Cancelled.")
			return nil
		}
	}

	var removed int
	for _, p := range toRemove {
		wt := p.worktree
		if !skipTeardown {
			if err := project.RunTeardownHooks(ctx, cfg, wt.Path, IsDryRun()); err != nil {
				ui.Warning("Teardown hooks failed for " + wt.Branch + ": " + err.Error())
			}
			if err := project.RunParallelTeardownHooks(ctx, cfg, wt.Path, IsDryRun()); err != nil {
				ui.Warning("Parallel teardown hooks failed for " + wt.Branch + ": " + err.Error())
			}
		}

		ui.Step("Removing worktree: " + wt.Branch)
		if err := runner.WorktreeRemove(ctx, wt.Path, force); err != nil {
			ui.Warning(fmt.Sprintf("Could not remove worktree %s: %s", wt.Branch, err))
			continue
		}

		// Only a true ancestor merge satisfies `git branch -d`. For squash and
		// rebase merges git still calls the branch unmerged, so hand the user
		// the exact command rather than force-deleting behind their back.
		if err := runner.BranchDelete(ctx, wt.Branch, false); err != nil {
			if p.method == git.MergeAncestor {
				ui.Warning("Could not delete branch: " + err.Error())
			} else {
				ui.Warning(fmt.Sprintf(
					"Branch %s kept: %s\n  If this is the expected \"not fully merged\" refusal, delete it with: git branch -D %s",
					wt.Branch, err, wt.Branch,
				))
			}
		}

		removed++
	}

	if err := runner.WorktreePrune(ctx); err != nil {
		ui.Warning("Could not prune worktree metadata: " + err.Error())
	}

	summary := fmt.Sprintf("Pruned %d worktree(s)", removed)
	if len(kept) > 0 {
		summary += fmt.Sprintf(", kept %d with uncommitted changes (use --force)", len(kept))
	}
	ui.Success(summary)
	return nil
}
