// Package git wraps git operations for bare repo worktree management.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/bkildow/wtx/internal/ui"
)

type WorktreeInfo struct {
	Path   string
	Branch string
	Head   string
	Bare   bool
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
	HasLocalBranch(ctx context.Context, branch string) (bool, error)
	WorktreeAdd(ctx context.Context, path, branch string) error
	WorktreeAddNew(ctx context.Context, path, branch, baseBranch string) error
	WorktreeRemove(ctx context.Context, path string, force bool) error
	WorktreeList(ctx context.Context) ([]WorktreeInfo, error)
	WorktreePrune(ctx context.Context) error
	BranchDelete(ctx context.Context, branch string, force bool) error
	IsWorktreeDirty(ctx context.Context, worktreePath string) (bool, error)
	IsBranchMerged(ctx context.Context, branch, target string) (bool, error)
	BranchMergeStatus(ctx context.Context, branch, target string) (MergeStatus, error)
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

// Query executes a read-only git command. Unlike Run, it executes even under
// --dry-run: dry-run suppresses the changes, but the analysis that decides
// *which* changes to propose still needs real answers. Stubbing queries out
// makes --dry-run report on an empty repository — see the commands that walk
// WorktreeList before deciding what to touch.
func (r *Runner) Query(ctx context.Context, args ...string) (string, error) {
	return r.queryWithEnv(ctx, nil, args...)
}

// queryWithEnv is Query with extra environment variables appended, for the few
// callers that need to steer git itself (an isolated object store, a synthetic
// commit identity) rather than just pass flags.
func (r *Runner) queryWithEnv(ctx context.Context, extraEnv []string, args ...string) (string, error) {
	fullArgs := append([]string{"--git-dir", r.GitDir}, args...)
	cmdStr := "git " + strings.Join(fullArgs, " ")

	ui.Command(cmdStr)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if r.BatchMode {
		cmd.Env = batchEnv()
	}
	if len(extraEnv) > 0 {
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		cmd.Env = append(cmd.Env, extraEnv...)
	}

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w\n%s", cmdStr, err, stderr.String())
	}

	return strings.TrimSpace(stdout.String()), nil
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

func (r *Runner) HasLocalBranch(ctx context.Context, branch string) (bool, error) {
	_, err := r.Query(ctx, "rev-parse", "--verify", "refs/heads/"+branch)
	if err != nil {
		return false, nil
	}
	return true, nil
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

func (r *Runner) BranchDelete(ctx context.Context, branch string, force bool) error {
	flag := "-d"
	if force {
		flag = "-D"
	}
	_, err := r.Run(ctx, "branch", flag, branch)
	return err
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
	args := []string{"--git-dir", r.GitDir, "merge-base", "--is-ancestor", branch, target}
	cmdStr := "git " + strings.Join(args, " ")

	ui.Command(cmdStr)
	cmd := exec.CommandContext(ctx, "git", args...)
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
// target, and by which route. It escalates through three checks because the
// cheap ancestor test only recognizes fast-forward and true merge commits:
// squash and rebase merges rewrite the commits, so the branch tip is never an
// ancestor of the target.
//
//  1. ancestor  — git merge-base --is-ancestor
//  2. rebase    — git cherry: patch-ids survive a rebase, so every commit on
//     the branch shows up as an equivalent ("-") commit in target. With a
//     single commit this is reported as MergeEquivalent, since a one-commit
//     squash produces exactly the same evidence.
//  3. squash    — a synthetic commit holding the branch's whole tree on top of
//     the merge base. Its patch-id equals the branch's combined diff, which is
//     exactly what a squash commit contains. Check 2 cannot find this for a
//     multi-commit branch, since no individual commit matches the squash.
//
// The squash probe needs a commit object to hand to git cherry. It is written
// to a throwaway object store (GIT_OBJECT_DIRECTORY) with the real repo as an
// alternate, so the managed repo is never modified — including under
// --dry-run, where the probe still has to run for prune to report the right
// set of worktrees. Any failure degrades to "not squash-merged" rather than
// failing the whole check.
func (r *Runner) BranchMergeStatus(ctx context.Context, branch, target string) (MergeStatus, error) {
	ancestor, err := r.IsBranchMerged(ctx, branch, target)
	if err != nil {
		return MergeStatus{}, err
	}
	if ancestor {
		return MergeStatus{Merged: true, Method: MergeAncestor}, nil
	}

	cherry, err := r.Query(ctx, "cherry", target, branch)
	if err != nil {
		return MergeStatus{}, err
	}
	if equivalent, commits := parseCherryEquivalent(cherry); equivalent {
		method := MergeRebase
		if commits == 1 {
			method = MergeEquivalent
		}
		return MergeStatus{Merged: true, Method: method}, nil
	}

	// A failing probe means "could not prove a squash merge", which is the same
	// actionable answer as "not squash-merged": the two cheaper checks have
	// already ruled out the other routes.
	if squashed, err := r.isSquashMerged(ctx, branch, target); err == nil && squashed {
		return MergeStatus{Merged: true, Method: MergeSquash}, nil
	}

	return MergeStatus{Merged: false, Method: MergeNone}, nil
}

func (r *Runner) isSquashMerged(ctx context.Context, branch, target string) (bool, error) {
	base, err := r.Query(ctx, "merge-base", target, branch)
	if err != nil {
		return false, err
	}
	if base == "" {
		return false, nil
	}

	branchTree, err := r.Query(ctx, "rev-parse", branch+"^{tree}")
	if err != nil {
		return false, err
	}
	baseTree, err := r.Query(ctx, "rev-parse", base+"^{tree}")
	if err != nil {
		return false, err
	}

	// No content change relative to the merge base: there is nothing to find in
	// the target, and probing with an empty patch would match spuriously.
	if branchTree == baseTree {
		return false, nil
	}

	probeDir, err := os.MkdirTemp("", "wt-merge-probe-")
	if err != nil {
		return false, err
	}
	defer func() { _ = os.RemoveAll(probeDir) }()

	env := probeEnv(probeDir, r.GitDir)

	probe, err := r.queryWithEnv(ctx, env, "commit-tree", branchTree, "-p", base, "-m", "wt-merge-probe")
	if err != nil {
		return false, err
	}

	out, err := r.queryWithEnv(ctx, env, "cherry", target, probe)
	if err != nil {
		return false, err
	}

	merged, _ := parseCherryEquivalent(out)
	return merged, nil
}

// probeEnv points git at a throwaway object store backed by the real repo, and
// supplies a fixed identity. git commit-tree refuses to run without user.name
// and user.email, which are routinely unset in CI and fresh containers, and the
// identity is arbitrary anyway: the probe commit exists only long enough for
// git cherry to compute its patch-id.
func probeEnv(probeDir, gitDir string) []string {
	return []string{
		"GIT_OBJECT_DIRECTORY=" + probeDir,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + filepath.Join(gitDir, "objects"),
		"GIT_AUTHOR_NAME=wt", "GIT_AUTHOR_EMAIL=wt@localhost",
		"GIT_COMMITTER_NAME=wt", "GIT_COMMITTER_EMAIL=wt@localhost",
		"GIT_AUTHOR_DATE=@0 +0000", "GIT_COMMITTER_DATE=@0 +0000",
	}
}

// parseCherryEquivalent reports whether every commit listed by `git cherry` has
// an equivalent in the upstream branch, along with how many commits were
// listed. git cherry prefixes each commit with "-" when an equivalent patch
// exists upstream and "+" when it does not, so a single "+" means unmerged work
// remains. Empty output means the branch has no commits of its own to account
// for, which is not evidence of a merge. The count lets callers tell a
// multi-commit rebase merge from the ambiguous single-commit case.
func parseCherryEquivalent(out string) (equivalent bool, commits int) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	count := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			return false, 0
		}
		count++
	}
	return count > 0, count
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
	if _, err := r.Query(ctx, "rev-parse", "--verify", "origin/"+branch); err == nil {
		return "origin/" + branch
	}
	if _, err := r.Query(ctx, "rev-parse", "--verify", branch); err == nil {
		return branch
	}
	return "HEAD"
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
