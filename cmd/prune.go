package cmd

import (
	"context"
	"fmt"
	"os"

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

// removeWorkers bounds concurrent `git worktree remove` calls. Removal is
// mostly deleting files, which contend in the filesystem; 8 workers measured
// fastest on macOS.
const removeWorkers = 8

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

	pruneable := findPrunable(ctx, runner, f, candidates, defaultBranch)

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

	// Dry-run removal costs nothing, and one worker keeps its notices in
	// worktree order.
	workers := removeWorkers
	if IsDryRun() {
		workers = 1
	}
	ui.Step(fmt.Sprintf("Removing %d worktree(s)", len(toRemove)))
	removedOK := removeConcurrently(toRemove, workers, func(wt git.WorktreeInfo) error {
		return runner.WorktreeRemove(ctx, wt.Path, force)
	})

	// Branches are deleted afterwards and one at a time: concurrent
	// `git branch -d` calls contend for packed-refs.lock.
	var removed, branchesKept int
	for i, p := range toRemove {
		if !removedOK[i] {
			continue
		}
		removed++

		// git branch -d refuses squash- and rebase-merged branches as unmerged.
		// Keep them and print the delete command rather than force-deleting.
		if deleteBranchOrKeep(ctx, runner, p.worktree.Branch) {
			branchesKept++
		}
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

// findPrunable returns the merged worktrees in their original order. git
// judges every branch first; the ones it cannot show merged then have their
// pull requests fetched in a single forge lookup.
func findPrunable(ctx context.Context, g mergeChecker, f forge.Forge, wts []git.WorktreeInfo, defaultBranch string) []prunable {
	statuses := make([]git.MergeStatus, len(wts))
	errs := make([]error, len(wts))
	checkConcurrently(len(wts), func(i int) {
		statuses[i], errs[i] = g.BranchMergeStatus(ctx, wts[i].Branch, defaultBranch)
	})

	// An empty reason means the branch has not been shown merged.
	reasons := make([]string, len(wts))
	var unmerged []string
	for i, wt := range wts {
		switch {
		case errs[i] != nil:
			ui.Warning(fmt.Sprintf("%s: could not check merge status: %s", wt.Branch, errs[i]))
		case statuses[i].Merged:
			reasons[i] = mergeReason(statuses[i].Method)
		default:
			unmerged = append(unmerged, wt.Branch)
		}
	}

	// Only branches git left unmerged were looked up, so a hit never
	// overrides git's own reason.
	prs := lookupPRs(ctx, f, unmerged)
	for i, wt := range wts {
		if pr := prs[wt.Branch]; prMerged(ctx, g, pr, wt, defaultBranch) {
			reasons[i] = fmt.Sprintf("merged (PR #%d)", pr.Number)
		}
	}

	// A worktree's dirtiness decides whether it is removed, so an unanswerable
	// question is treated as dirty rather than risking uncommitted work.
	dirty := make([]bool, len(wts))
	dirtyErrs := make([]error, len(wts))
	checkConcurrently(len(wts), func(i int) {
		if reasons[i] != "" {
			dirty[i], dirtyErrs[i] = g.IsWorktreeDirty(ctx, wts[i].Path)
		}
	})

	var out []prunable
	for i, wt := range wts {
		if reasons[i] == "" {
			continue
		}
		if dirtyErrs[i] != nil {
			ui.Warning(fmt.Sprintf("%s: could not check for uncommitted changes, assuming dirty: %s", wt.Branch, dirtyErrs[i]))
			dirty[i] = true
		}
		out = append(out, prunable{worktree: wt, reason: reasons[i], dirty: dirty[i]})
	}
	return out
}

// checkConcurrently runs check for each index below n, at most checkWorkers at
// a time. Each check writes only its own index, so results keep worktree order.
func checkConcurrently(n int, check func(i int)) {
	var pool errgroup.Group
	pool.SetLimit(checkWorkers)
	for i := range n {
		pool.Go(func() error {
			check(i)
			return nil
		})
	}
	_ = pool.Wait()
}

// lookupPRs fetches the pull requests for branches. Without a forge, or when
// the lookup fails (gh unauthenticated or offline), there are none, and the
// branches stand on git's verdict alone.
func lookupPRs(ctx context.Context, f forge.Forge, branches []string) map[string]*forge.PullRequest {
	if f == nil || len(branches) == 0 {
		return nil
	}
	prs, err := f.PRsForBranches(ctx, branches)
	if err != nil {
		ui.Warning(fmt.Sprintf("Could not check pull request state: %s\n  Branches are judged by git alone.", err))
		return nil
	}
	return prs
}

// removeConcurrently removes each worktree with at most workers removals in
// flight, reporting each one as it finishes so a long run shows progress. The
// result says, by index, which removals succeeded.
func removeConcurrently(items []prunable, workers int, remove func(git.WorktreeInfo) error) []bool {
	ok := make([]bool, len(items))
	var pool errgroup.Group
	pool.SetLimit(workers)
	for i, p := range items {
		pool.Go(func() error {
			wt := p.worktree
			if err := remove(wt); err != nil {
				ui.Warning(fmt.Sprintf("Could not remove worktree %s: %s", wt.Branch, err))
				return nil
			}
			ok[i] = true
			ui.Success("Removed worktree: " + wt.Branch)
			return nil
		})
	}
	_ = pool.Wait()
	return ok
}
