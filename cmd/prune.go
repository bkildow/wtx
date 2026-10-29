package cmd

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/bkildow/wtx/internal/forge"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
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

// checkWorkers bounds the concurrent merge checks. Each check is a few git
// processes plus, for an unmerged branch, a gh network round-trip, so the pool
// is sized to overlap those waits rather than to match the CPU count.
const checkWorkers = 8

// mergeChecker is the slice of git.Runner the merge checks need, so they can
// be exercised without a repository.
type mergeChecker interface {
	BranchMergeStatus(ctx context.Context, branch, target string) (git.MergeStatus, error)
	IsWorktreeDirty(ctx context.Context, worktreePath string) (bool, error)
	Query(ctx context.Context, args ...string) (string, error)
}

// prunable pairs a worktree with why it was judged merged, so the removal loop
// and the listing can both explain themselves.
type prunable struct {
	worktree git.WorktreeInfo
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
func prMerged(ctx context.Context, runner mergeChecker, pr *forge.PullRequest, wt git.WorktreeInfo, defaultBranch string) bool {
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

	clone, err := openClone(ctx)
	if err != nil {
		return err
	}
	projectRoot, cfg := clone.Root(), clone.Config()

	force, _ := cmd.Flags().GetBool("force")
	yes, _ := cmd.Flags().GetBool("yes")
	skipTeardown, _ := cmd.Flags().GetBool("skip-teardown")

	cwd, _ := os.Getwd()
	runner := clone.Runner()

	defaultBranch := cfg.MainBranchOrDefault()

	wts, err := clone.Worktrees(ctx)
	if err != nil {
		return err
	}
	filtered := wts.Managed

	// Resolve current worktree path for comparison
	currentPath := ui.CanonicalPath(cwd)

	// Optional PR awareness. A missing or unusable gh is the normal case, not
	// an error: detection just falls back to the git-native checks.
	var f forge.Forge
	if remoteURL, err := runner.RemoteURL(ctx, "origin"); err == nil {
		f = forge.Detect(ctx, remoteURL)
	}

	var candidates []git.WorktreeInfo
	for _, wt := range filtered {
		if wt.Branch == defaultBranch || ui.CanonicalPath(wt.Path) == currentPath {
			continue
		}
		candidates = append(candidates, wt)
	}

	pruneable := findPrunable(ctx, runner, &prDetection{forge: f}, candidates, defaultBranch)

	if len(pruneable) == 0 {
		ui.Info("No merged worktrees to prune.")
		return nil
	}

	ui.Step("Merged worktrees:")
	t := ui.NewTable().Headers("BRANCH", "PATH", "MERGED", "STATUS")
	paths := ui.NewPathDisplay(projectRoot)
	for _, p := range pruneable {
		relPath := paths.Path(p.worktree.Path)
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

	// Teardown hooks are user commands writing straight to the terminal, so
	// they run one worktree at a time to keep their output readable. Their
	// output does not name the worktree, so each one gets a heading.
	if !skipTeardown && cfg.HasTeardown() {
		for _, p := range toRemove {
			wt := p.worktree
			ui.Step("Tearing down worktree: " + wt.Branch)
			env := clone.ProjectEnv(project.NewTemplateVars(projectRoot, wt.Path, wt.Branch), wts)
			runTeardownHooks(ctx, cfg, env, IsDryRun())
		}
	}

	// Removal runs one worktree at a time: concurrent `git worktree remove`
	// calls race, as each one scans the repository's worktree metadata while
	// the others delete their entries, and fails with "Invalid path".
	ui.Step(fmt.Sprintf("Removing %d worktree(s)", len(toRemove)))
	var removed, branchesKept int
	for _, p := range toRemove {
		wt := p.worktree
		if err := runner.WorktreeRemove(ctx, wt.Path, force); err != nil {
			ui.Warning(fmt.Sprintf("Could not remove worktree %s: %s", wt.Branch, err))
			continue
		}
		removed++

		// git branch -d refuses squash- and rebase-merged branches as unmerged.
		// Keep them and print the delete command rather than force-deleting.
		kept := deleteBranchOrKeep(ctx, runner, wt.Branch)
		if kept {
			branchesKept++
		}
		ui.Success(removedWorktreeMessage(wt.Branch, kept))
	}

	if err := runner.WorktreePrune(ctx); err != nil {
		ui.Warning("Could not prune worktree metadata: " + err.Error())
	}

	summary := fmt.Sprintf("Pruned %d worktree(s)", removed)
	if branchesKept > 0 {
		summary += fmt.Sprintf(", kept %d branch(es) git calls unmerged", branchesKept)
	}
	if len(kept) > 0 {
		summary += fmt.Sprintf(", kept %d with uncommitted changes (use --force)", len(kept))
	}
	ui.Success(summary)
	return nil
}

// prDetection is pull request lookup shared by the concurrent merge checks.
// One gh failure usually means gh is unauthenticated or offline, so the first
// one turns lookups off for the rest of the run rather than repeating it for
// every worktree, and is reported once.
type prDetection struct {
	forge    forge.Forge
	disabled atomic.Bool
}

// lookup returns branch's pull request, or nil when detection is off or the
// lookup failed. warning is set only for the failure that turned detection off.
func (d *prDetection) lookup(ctx context.Context, branch string) (pr *forge.PullRequest, warning string) {
	if d.forge == nil || d.disabled.Load() {
		return nil, ""
	}
	pr, err := d.forge.PRForBranch(ctx, branch)
	if err == nil {
		return pr, ""
	}
	// Only the lookup that turns detection off says so, since the remaining
	// worktrees are now judged by git alone.
	if d.disabled.CompareAndSwap(false, true) {
		warning = fmt.Sprintf(
			"%s: could not check pull request state: %s\n  Pull request detection disabled for the rest of this run.",
			branch, err,
		)
	}
	return nil, warning
}

// pruneCheck is one worktree's verdict. Warnings are held rather than printed
// so they come out in worktree order, not in the order the checks finish.
type pruneCheck struct {
	candidate *prunable
	warnings  []string
}

// findPrunable checks worktrees concurrently and returns the merged ones in
// their original order.
func findPrunable(ctx context.Context, g mergeChecker, prs *prDetection, wts []git.WorktreeInfo, defaultBranch string) []prunable {
	checks := make([]pruneCheck, len(wts))
	var pool errgroup.Group
	pool.SetLimit(checkWorkers)
	for i, wt := range wts {
		pool.Go(func() error {
			checks[i] = checkWorktree(ctx, g, prs, wt, defaultBranch)
			return nil
		})
	}
	_ = pool.Wait()

	var out []prunable
	for _, c := range checks {
		for _, w := range c.warnings {
			ui.Warning(w)
		}
		if c.candidate != nil {
			out = append(out, *c.candidate)
		}
	}
	return out
}

// checkWorktree decides whether wt's branch is merged into defaultBranch,
// first by git and then, failing that, by its pull request.
func checkWorktree(ctx context.Context, g mergeChecker, prs *prDetection, wt git.WorktreeInfo, defaultBranch string) pruneCheck {
	var c pruneCheck

	status, err := g.BranchMergeStatus(ctx, wt.Branch, defaultBranch)
	if err != nil {
		c.warnings = append(c.warnings, fmt.Sprintf("%s: could not check merge status: %s", wt.Branch, err))
		return c
	}

	reason := mergeReason(status.Method)
	if !status.Merged {
		pr, warning := prs.lookup(ctx, wt.Branch)
		if warning != "" {
			c.warnings = append(c.warnings, warning)
		}
		if !prMerged(ctx, g, pr, wt, defaultBranch) {
			return c
		}
		reason = fmt.Sprintf("merged (PR #%d)", pr.Number)
	}

	// A worktree's dirtiness decides whether it is removed, so an unanswerable
	// question is treated as dirty rather than risking uncommitted work.
	dirty, err := g.IsWorktreeDirty(ctx, wt.Path)
	if err != nil {
		c.warnings = append(c.warnings, fmt.Sprintf("%s: could not check for uncommitted changes, assuming dirty: %s", wt.Branch, err))
		dirty = true
	}

	c.candidate = &prunable{worktree: wt, reason: reason, dirty: dirty}
	return c
}
