// Package git wraps git operations for bare repo worktree management.
package git

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/bkildow/wtx/internal/ui"
	"golang.org/x/sync/errgroup"
)

type WorktreeInfo struct {
	Path     string
	Branch   string
	Head     string
	Bare     bool
	Locked   bool
	Prunable bool
}

type Git interface {
	Run(ctx context.Context, args ...string) (string, error)
	Query(ctx context.Context, args ...string) (string, error)
	CloneBare(ctx context.Context, url, dest string) error
	ConfigureRemoteFetch(ctx context.Context) error
	EnableWorktreeConfig(ctx context.Context) error
	SetWorktreeBareFalse(ctx context.Context, worktreePath string) error
	Fetch(ctx context.Context, remote string) error
	ListRemoteBranches(ctx context.Context) ([]string, error)
	HasRemoteBranch(ctx context.Context, branch string) (bool, error)
	HasLocalBranch(ctx context.Context, branch string) bool
	WorktreeAdd(ctx context.Context, path, branch string) error
	WorktreeAddNew(ctx context.Context, path, branch, baseBranch string) error
	FinishWorktree(ctx context.Context, path, branch string) error
	WorktreeRemove(ctx context.Context, path string, force bool) error
	WorktreeList(ctx context.Context) ([]WorktreeInfo, error)
	WorktreePrune(ctx context.Context) error
	BranchDelete(ctx context.Context, branch string, force bool) error
	IsWorktreeDirty(ctx context.Context, worktreePath string) (bool, error)
	IsBranchMerged(ctx context.Context, branch, target string) (bool, error)
	RemoteURL(ctx context.Context, remote string) (string, error)
	FetchAll(ctx context.Context) error
	GetDefaultBranch(ctx context.Context) (string, error)
	GetLastCommitAge(ctx context.Context, worktreePath string) (string, error)
	GetBehindCount(ctx context.Context, worktreePath string) (int, error)
	Pull(ctx context.Context, worktreePath string) error
	PullRebase(ctx context.Context, worktreePath string) error
}

type Runner struct {
	GitDir    string
	DryRun    bool
	BatchMode bool // Suppress interactive prompts (for non-TTY environments like hooks)
	Quiet     bool // Suppress diagnostic command output for structured reports.

	worktreeConfigEnabled bool

	versionOnce   sync.Once
	versionParsed [3]int
	versionErr    error
}

func NewRunner(gitDir string, dryRun bool) *Runner {
	return &Runner{GitDir: gitDir, DryRun: dryRun}
}

// batchEnv returns environment variables that suppress interactive git prompts.
func batchEnv() []string {
	env := os.Environ()
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	env = append(env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	return env
}

// Run executes a git command that may change repository state. Under --dry-run
// it prints the command and returns empty output without executing.
func (r *Runner) Run(ctx context.Context, args ...string) (string, error) {
	if r.DryRun {
		fullArgs := append([]string{"--git-dir", r.GitDir}, args...)
		ui.DryRunNotice("git " + strings.Join(fullArgs, " "))
		return "", nil
	}

	return r.Query(ctx, args...)
}

// runWithEnv is Run with extra environment variables, for mutating commands
// whose output the caller has to parse.
func (r *Runner) runWithEnv(ctx context.Context, extraEnv []string, args ...string) (string, error) {
	if r.DryRun {
		return r.Run(ctx, args...)
	}
	return r.queryWithEnv(ctx, extraEnv, args...)
}

// Query executes a read-only git command. Unlike Run, it executes even under
// --dry-run: dry-run suppresses the changes, but the analysis that decides
// *which* changes to propose still needs real answers. Stubbing queries out
// makes --dry-run report on an empty repository — see the commands that walk
// WorktreeList before deciding what to touch.
func (r *Runner) Query(ctx context.Context, args ...string) (string, error) {
	return r.queryWithEnv(ctx, nil, args...)
}

// queryWithEnv is Query with extra environment variables appended, for the few
// callers that need to steer git itself (such as its locale) rather than just
// pass flags.
func (r *Runner) queryWithEnv(ctx context.Context, extraEnv []string, args ...string) (string, error) {
	out, err := r.queryRaw(ctx, extraEnv, args...)
	return strings.TrimSpace(out), err
}

// QueryRaw is Query without whitespace trimming, for NUL-delimited output
// (e.g. ls-files -z) where leading or trailing spaces belong to a path.
func (r *Runner) QueryRaw(ctx context.Context, args ...string) (string, error) {
	return r.queryRaw(ctx, nil, args...)
}

// command builds a git command against the Runner's repository, returned with
// the form it is logged as.
func (r *Runner) command(ctx context.Context, args ...string) (cmd *exec.Cmd, logged string) {
	fullArgs := append([]string{"--git-dir", r.GitDir}, args...)
	cmd = exec.CommandContext(ctx, "git", fullArgs...)
	if r.BatchMode {
		cmd.Env = batchEnv()
	}
	return cmd, "git " + strings.Join(fullArgs, " ")
}

func (r *Runner) queryRaw(ctx context.Context, extraEnv []string, args ...string) (string, error) {
	cmd, cmdStr := r.command(ctx, args...)
	if !r.Quiet {
		ui.Command(cmdStr)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if len(extraEnv) > 0 {
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		cmd.Env = append(cmd.Env, extraEnv...)
	}

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w\n%s", cmdStr, err, stderr.String())
	}

	return stdout.String(), nil
}

func (r *Runner) CloneBare(ctx context.Context, url, dest string) error {
	args := []string{"clone", "--bare", url, dest}
	cmdStr := "git " + strings.Join(args, " ")

	if r.DryRun {
		ui.DryRunNotice(cmdStr)
		return nil
	}

	ui.Command(cmdStr)
	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone --bare: %w\n%s", err, stderr.String())
	}

	return nil
}

func (r *Runner) ConfigureRemoteFetch(ctx context.Context) error {
	_, err := r.Run(ctx, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	return err
}

// EnableWorktreeConfig enables extensions.worktreeConfig on the common dir.
// Idempotent and cached per Runner so repeated calls within one invocation
// don't re-fork.
func (r *Runner) EnableWorktreeConfig(ctx context.Context) error {
	if r.worktreeConfigEnabled {
		return nil
	}
	if _, err := r.Run(ctx, "config", "extensions.worktreeConfig", "true"); err != nil {
		return err
	}
	r.worktreeConfigEnabled = true
	return nil
}

// SetWorktreeBareFalse writes core.bare=false into the worktree's
// config.worktree. Required on git 2.52+ for worktrees attached to a bare
// common dir. Caller must have enabled extensions.worktreeConfig first.
func (r *Runner) SetWorktreeBareFalse(ctx context.Context, worktreePath string) error {
	args := []string{"-C", worktreePath, "config", "--worktree", "core.bare", "false"}
	cmdStr := "git " + strings.Join(args, " ")

	if r.DryRun {
		ui.DryRunNotice(cmdStr)
		return nil
	}

	ui.Command(cmdStr)
	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n%s", cmdStr, err, stderr.String())
	}

	return nil
}

func (r *Runner) Fetch(ctx context.Context, remote string) error {
	_, err := r.Run(ctx, "fetch", remote)
	return err
}

func (r *Runner) ListRemoteBranches(ctx context.Context) ([]string, error) {
	output, err := r.Query(ctx, "branch", "-r")
	if err != nil {
		return nil, err
	}

	return parseRemoteBranches(output), nil
}

func parseRemoteBranches(output string) []string {
	if output == "" {
		return nil
	}

	var branches []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip HEAD pointer lines like "origin/HEAD -> origin/main"
		if strings.Contains(line, "->") {
			continue
		}
		// Strip "origin/" prefix
		branch := strings.TrimPrefix(line, "origin/")
		branches = append(branches, branch)
	}

	return branches
}

func (r *Runner) HasRemoteBranch(ctx context.Context, branch string) (bool, error) {
	branches, err := r.ListRemoteBranches(ctx)
	if err != nil {
		return false, err
	}
	for _, b := range branches {
		if b == branch {
			return true, nil
		}
	}
	return false, nil
}

// HasLocalBranch reports whether refs/heads/<branch> exists; a failed lookup
// means there is no such branch.
func (r *Runner) HasLocalBranch(ctx context.Context, branch string) bool {
	_, err := r.Query(ctx, "rev-parse", "--verify", "refs/heads/"+branch)
	return err == nil
}

// Version returns the local git version as [major, minor, patch].
// The result is cached for the lifetime of the Runner so repeated calls
// don't re-fork. The probe runs `git --version` directly (not through
// r.Run) so it works before the bare repo exists, e.g. during `wt clone`.
func (r *Runner) Version(ctx context.Context) ([3]int, error) {
	r.versionOnce.Do(func() {
		cmd := exec.CommandContext(ctx, "git", "--version")
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			r.versionErr = fmt.Errorf("git --version: %w\n%s", err, stderr.String())
			return
		}
		r.versionParsed, r.versionErr = parseGitVersion(stdout.String())
	})
	return r.versionParsed, r.versionErr
}

// parseGitVersion extracts [major, minor, patch] from `git --version` output
// like "git version 2.52.0", "git version 2.39.5 (Apple Git-154)", or
// "git version 2.48.0.rc1". Trailing components beyond patch are ignored.
func parseGitVersion(output string) ([3]int, error) {
	s := strings.TrimSpace(output)
	s = strings.TrimPrefix(s, "git version ")
	// Split off anything after the first non-digit/non-dot rune.
	end := len(s)
	for i, c := range s {
		if (c < '0' || c > '9') && c != '.' {
			end = i
			break
		}
	}
	s = s[:end]
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return [3]int{}, fmt.Errorf("unrecognized git version output: %q", output)
	}
	var v [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [3]int{}, fmt.Errorf("unrecognized git version output: %q", output)
		}
		v[i] = n
	}
	return v, nil
}

// supportsRelativePaths reports whether the given git version accepts
// `git worktree add --relative-paths` (added in git 2.48).
func supportsRelativePaths(v [3]int) bool {
	return v[0] > 2 || (v[0] == 2 && v[1] >= 48)
}

// worktreeAddArgs builds the args slice for `git worktree add`, including
// --relative-paths only when the local git supports it. On version-probe
// error we omit the flag — falling back to absolute paths is always safe.
func (r *Runner) worktreeAddArgs(ctx context.Context, tail ...string) []string {
	args := []string{"worktree", "add"}
	if v, err := r.Version(ctx); err == nil && supportsRelativePaths(v) {
		args = append(args, "--relative-paths")
	}
	return append(args, tail...)
}

func (r *Runner) WorktreeAdd(ctx context.Context, path, branch string) error {
	if _, err := r.Run(ctx, r.worktreeAddArgs(ctx, path, branch)...); err != nil {
		return err
	}
	return r.FinishWorktree(ctx, path, branch)
}

// FinishWorktree makes a checkout that `git worktree add` just created usable
// by wtx, whoever ran it: core.bare=false in its config.worktree, and
// upstream tracking when origin/<branch> exists.
func (r *Runner) FinishWorktree(ctx context.Context, path, branch string) error {
	if err := r.EnableWorktreeConfig(ctx); err != nil {
		return err
	}
	if err := r.SetWorktreeBareFalse(ctx, path); err != nil {
		return err
	}
	// Set up tracking when the branch exists upstream. A bare clone copies
	// remote branches into refs/heads/* without tracking config, and
	// `git worktree add` won't add it when the local branch already exists, so
	// @{upstream}-dependent features (wt status behind-count, wt pull) break
	// without this. Best-effort: a failure here shouldn't fail the worktree.
	if _, err := r.Run(ctx, "rev-parse", "--verify", "origin/"+branch); err == nil {
		if err := r.SetUpstream(ctx, branch); err != nil {
			ui.Warning("Could not set upstream for " + branch + ": " + err.Error())
		}
	}
	return nil
}

// SetUpstream points the local branch at origin/<branch> so that @{upstream},
// `wt status` behind-counts, and `wt pull` work.
func (r *Runner) SetUpstream(ctx context.Context, branch string) error {
	_, err := r.Run(ctx, "branch", "--set-upstream-to=origin/"+branch, branch)
	return err
}

func (r *Runner) WorktreeAddNew(ctx context.Context, path, branch, baseBranch string) error {
	if _, err := r.Run(ctx, r.worktreeAddArgs(ctx, "-b", branch, path, baseBranch)...); err != nil {
		return err
	}
	if err := r.EnableWorktreeConfig(ctx); err != nil {
		return err
	}
	return r.SetWorktreeBareFalse(ctx, path)
}

func (r *Runner) WorktreeRemove(ctx context.Context, path string, force bool) error {
	args := []string{"worktree", "remove", path}
	if force {
		args = append(args, "--force")
	}
	_, err := r.Run(ctx, args...)
	return err
}

// WorktreeMove moves a linked worktree from src to dest.
func (r *Runner) WorktreeMove(ctx context.Context, src, dest string) error {
	_, err := r.Run(ctx, "worktree", "move", src, dest)
	return err
}

// WorktreeRepair repairs the links of the worktrees at paths, keeping them
// relative (as WorktreeAdd creates them) where git supports it.
func (r *Runner) WorktreeRepair(ctx context.Context, paths ...string) error {
	args := []string{"worktree", "repair"}
	if v, err := r.Version(ctx); err == nil && supportsRelativePaths(v) {
		args = append(args, "--relative-paths")
	}
	_, err := r.Run(ctx, append(args, paths...)...)
	return err
}

func (r *Runner) WorktreeList(ctx context.Context) ([]WorktreeInfo, error) {
	output, err := r.Query(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	return parseWorktreeList(output), nil
}

func parseWorktreeList(output string) []WorktreeInfo {
	if output == "" {
		return nil
	}

	var worktrees []WorktreeInfo
	var current WorktreeInfo

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "worktree "):
			current = WorktreeInfo{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "bare":
			current.Bare = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			current.Locked = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			current.Prunable = true
		case line == "":
			if current.Path != "" {
				worktrees = append(worktrees, current)
				current = WorktreeInfo{}
			}
		}
	}

	// Append the last entry if output doesn't end with a blank line
	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees
}

// ErrBranchNotMerged is returned by BranchDelete when `git branch -d` refuses
// because the branch is not fully merged. It replaces git's own message, whose
// hint lines suggest a command that doesn't run from a Bare layout's Project root.
var ErrBranchNotMerged = errors.New("branch is not fully merged")

func (r *Runner) BranchDelete(ctx context.Context, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	// LC_ALL=C keeps git's refusal in English so isNotFullyMerged can spot it.
	_, err := r.runWithEnv(ctx, []string{"LC_ALL=C"}, "branch", flag, branch)
	if err != nil && isNotFullyMerged(err.Error()) {
		return fmt.Errorf("%s: %w", branch, ErrBranchNotMerged)
	}
	return err
}

// isNotFullyMerged reports whether git's output is the `branch -d` refusal
// for an unmerged branch.
func isNotFullyMerged(output string) bool {
	return strings.Contains(output, "is not fully merged")
}

func (r *Runner) IsWorktreeDirty(ctx context.Context, worktreePath string) (bool, error) {
	args := []string{"-C", worktreePath, "status", "--porcelain"}
	cmdStr := "git " + strings.Join(args, " ")

	ui.Command(cmdStr)
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return false, fmt.Errorf("%s: %w\n%s", cmdStr, err, stderr.String())
	}

	return parseDirtyStatus(stdout.String()), nil
}

func parseDirtyStatus(output string) bool {
	return strings.TrimSpace(output) != ""
}

func (r *Runner) IsBranchMerged(ctx context.Context, branch, target string) (bool, error) {
	cmd, cmdStr := r.command(ctx, "merge-base", "--is-ancestor", branch, target)
	if !r.Quiet {
		ui.Command(cmdStr)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.ExitCode() == 1 {
				return false, nil
			}
		}
		return false, fmt.Errorf("%s: %w\n%s", cmdStr, err, stderr.String())
	}

	return true, nil
}

// MergeMethod identifies how a branch's work reached the target branch. The
// distinction matters because only MergeAncestor satisfies `git branch -d`.
type MergeMethod string

const (
	MergeNone     MergeMethod = ""
	MergeAncestor MergeMethod = "ancestor"
	MergeRebase   MergeMethod = "rebase"
	MergeSquash   MergeMethod = "squash"
	// MergeEquivalent covers a single-commit branch whose commit has an
	// equivalent in the target. Squash and rebase merges are indistinguishable
	// in that case, so neither label would be honest.
	MergeEquivalent MergeMethod = "equivalent"
)

// MergeStatus is the result of BranchMergeStatus.
type MergeStatus struct {
	Merged bool
	Method MergeMethod
}

// BranchMergeStatus reports whether branch's work is already contained in
// target, and by which route. It is BranchMergeStatuses for a single branch.
func (r *Runner) BranchMergeStatus(ctx context.Context, branch, target string) (MergeStatus, error) {
	statuses, errs := r.BranchMergeStatuses(ctx, []string{branch}, target)
	return statuses[0], errs[0]
}

// mergeWorkers bounds the branches BranchMergeStatuses inspects at once.
const mergeWorkers = 8

// BranchMergeStatuses reports, for each branch, whether its work is already
// contained in target, and by which route. It escalates through three checks
// because the cheap ancestor test only recognizes fast-forward and true merge
// commits: squash and rebase merges rewrite the commits, so the branch tip is
// never an ancestor of the target.
//
//  1. ancestor  — git merge-base --is-ancestor
//  2. rebase    — patch-ids survive a rebase, so every commit on the branch
//     has an equivalent among the target's commits (what git cherry reports).
//     With a single commit this is reported as MergeEquivalent, since a
//     one-commit squash produces exactly the same evidence.
//  3. squash    — the patch-id of the branch's whole diff against the merge
//     base, which is exactly what a squash commit contains. Check 2 cannot
//     find this for a multi-commit branch, since no individual commit matches
//     the squash.
//
// Checks 2 and 3 compare against the target commits each branch lacks, which
// overlap heavily when many branches fork from one long-lived target. Every
// commit's patch-id is therefore computed once, in a single git patch-id run,
// rather than once per branch and check as running git cherry would.
//
// Nothing is written to the repository, so the checks run under --dry-run.
// A failed squash probe degrades to "not squash-merged" rather than failing
// the whole check.
func (r *Runner) BranchMergeStatuses(ctx context.Context, branches []string, target string) ([]MergeStatus, []error) {
	candidates := make([]mergeCandidate, len(branches))
	errs := make([]error, len(branches))
	var pool errgroup.Group
	pool.SetLimit(mergeWorkers)
	for i, branch := range branches {
		pool.Go(func() error {
			candidates[i], errs[i] = r.mergeCandidate(ctx, branch, target)
			return nil
		})
	}
	_ = pool.Wait()

	var commits []string
	seen := make(map[string]bool)
	for i, c := range candidates {
		if errs[i] != nil {
			continue
		}
		for _, commit := range slices.Concat(c.upstream, c.own) {
			if !seen[commit] {
				seen[commit] = true
				commits = append(commits, commit)
			}
		}
	}

	ids, err := r.patchIDs(ctx, commits)
	statuses := make([]MergeStatus, len(branches))
	for i, c := range candidates {
		if errs[i] != nil {
			continue
		}
		if c.ancestor {
			statuses[i] = MergeStatus{Merged: true, Method: MergeAncestor}
			continue
		}
		branchIDs := ids
		if err != nil {
			// The shared run failed, perhaps on one unreadable commit. Retrying
			// each branch alone fails only the branches whose ranges hold it.
			if branchIDs, errs[i] = r.patchIDs(ctx, slices.Concat(c.upstream, c.own)); errs[i] != nil {
				continue
			}
		}
		statuses[i] = c.status(branchIDs)
	}
	return statuses, errs
}

// mergeCandidate is what the patch-id checks need to know about one branch.
type mergeCandidate struct {
	ancestor bool
	upstream []string // non-merge commits in target that branch lacks
	own      []string // non-merge commits in branch that target lacks
	probe    string   // patch-id of the branch's whole diff, "" when none
}

func (r *Runner) mergeCandidate(ctx context.Context, branch, target string) (mergeCandidate, error) {
	ancestor, err := r.IsBranchMerged(ctx, branch, target)
	if err != nil || ancestor {
		return mergeCandidate{ancestor: ancestor}, err
	}

	// The same commit ranges git cherry target branch compares.
	var c mergeCandidate
	if c.upstream, err = r.revList(ctx, target, branch); err != nil {
		return c, err
	}
	if c.own, err = r.revList(ctx, branch, target); err != nil {
		return c, err
	}

	// A failing probe means "could not prove a squash merge", which is the same
	// actionable answer as "not squash-merged".
	c.probe, _ = r.squashProbe(ctx, branch, target)
	return c, nil
}

// status runs the rebase and squash checks against ids, the patch-ids of
// every commit in c. A commit with no patch-id (an empty commit) has no
// equivalent, which errs towards keeping the branch.
func (c mergeCandidate) status(ids map[string]string) MergeStatus {
	upstream := make(map[string]bool, len(c.upstream))
	for _, commit := range c.upstream {
		if id := ids[commit]; id != "" {
			upstream[id] = true
		}
	}

	// No commits of its own is not evidence of a merge.
	if len(c.own) > 0 && !slices.ContainsFunc(c.own, func(commit string) bool { return !upstream[ids[commit]] }) {
		if len(c.own) == 1 {
			return MergeStatus{Merged: true, Method: MergeEquivalent}
		}
		return MergeStatus{Merged: true, Method: MergeRebase}
	}

	if c.probe != "" && upstream[c.probe] {
		return MergeStatus{Merged: true, Method: MergeSquash}
	}
	return MergeStatus{Merged: false, Method: MergeNone}
}

// revList lists the non-merge commits reachable from include but not from
// exclude. git cherry skips merge commits on both sides too.
func (r *Runner) revList(ctx context.Context, include, exclude string) ([]string, error) {
	out, err := r.Query(ctx, "rev-list", "--no-merges", include, "^"+exclude, "--")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// squashProbe returns the patch-id of branch's whole diff against its merge
// base with target, or "" when the branch changes nothing: probing with an
// empty patch would match spuriously.
func (r *Runner) squashProbe(ctx context.Context, branch, target string) (string, error) {
	base, err := r.Query(ctx, "merge-base", target, branch)
	if err != nil || base == "" {
		return "", err
	}
	out, err := r.patchIDPipe(ctx, "", "diff-tree", "-p", "--full-index", "--no-renames", base, branch)
	if err != nil {
		return "", err
	}
	id, _, _ := strings.Cut(out, " ")
	return id, nil
}

// patchIDs returns the patch-id of each of commits, keyed by commit. Empty
// commits have none. Commits and the squash probe go through the same diff
// options, so their ids are comparable.
func (r *Runner) patchIDs(ctx context.Context, commits []string) (map[string]string, error) {
	ids := make(map[string]string, len(commits))
	if len(commits) == 0 {
		return ids, nil
	}
	out, err := r.patchIDPipe(ctx, strings.Join(commits, "\n")+"\n",
		"diff-tree", "--stdin", "-p", "--full-index", "--no-renames", "--root")
	if err != nil {
		return nil, err
	}
	for line := range strings.Lines(out) {
		if id, commit, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			ids[commit] = id
		}
	}
	return ids, nil
}

// patchIDPipe runs `git <diffArgs>`, fed stdin, and pipes its patch straight
// into `git patch-id --stable`, returning that output. patch-id hashes a
// binary change by the blob ids on its index line, and --full-index keeps two
// different changes to one binary file from sharing a patch-id through
// abbreviated ids. diff-tree, being plumbing, ignores diff config such as
// external diff drivers that would change the patch text.
func (r *Runner) patchIDPipe(ctx context.Context, stdin string, diffArgs ...string) (string, error) {
	diff, diffStr := r.command(ctx, diffArgs...)
	id, idStr := r.command(ctx, "patch-id", "--stable")
	cmdStr := diffStr + " | " + idStr
	if !r.Quiet {
		ui.Command(cmdStr)
	}

	diff.Stdin = strings.NewReader(stdin)
	var stdout, diffErr, idErr bytes.Buffer
	diff.Stderr, id.Stdout, id.Stderr = &diffErr, &stdout, &idErr

	pipe, err := diff.StdoutPipe()
	if err != nil {
		return "", err
	}
	id.Stdin = pipe
	if err := diff.Start(); err != nil {
		return "", fmt.Errorf("%s: %w", cmdStr, err)
	}
	idRunErr := id.Run()
	// Should patch-id exit before draining the patch, closing the pipe stops
	// diff-tree blocking on a full buffer, which would hang the Wait.
	_ = pipe.Close()
	diffRunErr := diff.Wait()
	// A failed patch-id leaves diff-tree dying of a broken pipe, so its error
	// is the cause; a failed diff-tree leaves patch-id succeeding on short input.
	if err := cmp.Or(idRunErr, diffRunErr); err != nil {
		return "", fmt.Errorf("%s: %w\n%s%s", cmdStr, err, diffErr.String(), idErr.String())
	}
	return stdout.String(), nil
}

// RemoteURL returns the configured URL for a remote, e.g. to detect which
// forge (if any) backs the repository.
func (r *Runner) RemoteURL(ctx context.Context, remote string) (string, error) {
	return r.Query(ctx, "remote", "get-url", remote)
}

func (r *Runner) FetchAll(ctx context.Context) error {
	_, err := r.Run(ctx, "fetch", "--all")
	return err
}

func (r *Runner) GetDefaultBranch(ctx context.Context) (string, error) {
	output, err := r.Query(ctx, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil {
		return parseDefaultBranch(output), nil
	}

	if _, err := r.Query(ctx, "show-ref", "--verify", "--quiet", "refs/heads/main"); err == nil {
		return "main", nil
	}

	if _, err := r.Query(ctx, "show-ref", "--verify", "--quiet", "refs/heads/master"); err == nil {
		return "master", nil
	}

	return "", fmt.Errorf("could not determine default branch")
}

// ResolveStartPoint finds a valid git ref for the given branch name.
// It tries origin/<branch> first (for bare repos), then the local branch,
// and falls back to HEAD if neither exists.
func (r *Runner) ResolveStartPoint(ctx context.Context, branch string) string {
	if r.refExists(ctx, "origin/"+branch) {
		return "origin/" + branch
	}
	if r.refExists(ctx, branch) {
		return branch
	}
	return "HEAD"
}

// ResolveRef finds the git ref to start a new branch from for a user-supplied
// name. A local branch with commits not on origin wins so unpushed work isn't
// silently dropped; otherwise origin/<ref> is preferred because local branches
// in a bare clone are usually stale copies from clone time. Tags, commits and
// fully qualified names like origin/develop fall through to <ref> itself.
func (r *Runner) ResolveRef(ctx context.Context, ref string) (string, error) {
	if ref == "HEAD" {
		// HEAD in the bare repo is the clone-time default branch, not the
		// worktree the user is standing in.
		return "", fmt.Errorf("HEAD is ambiguous in a bare repo; name a branch, tag, or commit")
	}

	hasRemote := r.refExists(ctx, "refs/remotes/origin/"+ref)
	hasLocal := r.refExists(ctx, "refs/heads/"+ref)

	if hasLocal && hasRemote {
		out, err := r.Query(ctx, "rev-list", "--count", "refs/remotes/origin/"+ref+"..refs/heads/"+ref)
		if err == nil && strings.TrimSpace(out) != "0" {
			return ref, nil
		}
	}
	if hasRemote {
		return "origin/" + ref, nil
	}
	if r.refExists(ctx, ref) {
		return ref, nil
	}
	return "", fmt.Errorf("ref %q not found locally or on origin", ref)
}

func (r *Runner) refExists(ctx context.Context, ref string) bool {
	_, err := r.Query(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

func (r *Runner) WorktreePrune(ctx context.Context) error {
	_, err := r.Run(ctx, "worktree", "prune")
	return err
}

func (r *Runner) GetLastCommitAge(ctx context.Context, worktreePath string) (string, error) {
	args := []string{"-C", worktreePath, "log", "-1", "--format=%cr"}
	cmdStr := "git " + strings.Join(args, " ")

	ui.Command(cmdStr)
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w\n%s", cmdStr, err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
}

func (r *Runner) GetBehindCount(ctx context.Context, worktreePath string) (int, error) {
	checkArgs := []string{"-C", worktreePath, "rev-parse", "--verify", "--quiet", "@{upstream}"}
	checkStr := "git " + strings.Join(checkArgs, " ")

	ui.Command(checkStr)
	if err := exec.CommandContext(ctx, "git", checkArgs...).Run(); err != nil {
		return 0, nil
	}

	args := []string{"-C", worktreePath, "rev-list", "--count", "HEAD..@{upstream}"}
	cmdStr := "git " + strings.Join(args, " ")

	ui.Command(cmdStr)
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("%s: %w\n%s", cmdStr, err, stderr.String())
	}

	return parseBehindCount(stdout.String()), nil
}

func (r *Runner) Pull(ctx context.Context, worktreePath string) error {
	args := []string{"-C", worktreePath, "pull"}
	cmdStr := "git " + strings.Join(args, " ")

	if r.DryRun {
		ui.DryRunNotice(cmdStr)
		return nil
	}

	ui.Command(cmdStr)
	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n%s", cmdStr, err, stderr.String())
	}

	return nil
}

func (r *Runner) PullRebase(ctx context.Context, worktreePath string) error {
	args := []string{"-C", worktreePath, "pull", "--rebase"}
	cmdStr := "git " + strings.Join(args, " ")

	if r.DryRun {
		ui.DryRunNotice(cmdStr)
		return nil
	}

	ui.Command(cmdStr)
	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w\n%s", cmdStr, err, stderr.String())
	}

	return nil
}

func parseDefaultBranch(output string) string {
	s := strings.TrimSpace(output)
	return strings.TrimPrefix(s, "refs/remotes/origin/")
}

func parseBranchList(output string) []string {
	if output == "" {
		return nil
	}

	var branches []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "* ")
		branches = append(branches, line)
	}

	return branches
}

func parseBehindCount(output string) int {
	n, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		return 0
	}
	return n
}

// LocalConfig reads key from the repository's own config file (the common
// directory's config, shared by all linked worktrees). An unset key reports
// ok == false without an error.
func (r *Runner) LocalConfig(ctx context.Context, key string) (value string, ok bool, err error) {
	out, err := r.Query(ctx, "config", "--local", "--get", key)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return out, true, nil
}

// SetLocalConfig writes key to the repository's own config file. Under
// --dry-run it only prints the command.
func (r *Runner) SetLocalConfig(ctx context.Context, key, value string) error {
	_, err := r.Run(ctx, "config", "--local", key, value)
	return err
}
