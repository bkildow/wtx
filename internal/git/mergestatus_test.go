package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/ui"
)

// mergeRepo is a scratch git repo for the merge-detection tests, with helpers
// that keep the branch topology readable.
type mergeRepo struct {
	t   *testing.T
	dir string
}

func newMergeRepo(t *testing.T) *mergeRepo {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	ui.Output = os.Stderr

	r := &mergeRepo{t: t, dir: t.TempDir()}
	r.git("init", "-q", "-b", "main")
	r.commit("base.txt", "base\n")
	return r
}

func (r *mergeRepo) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(
		os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func (r *mergeRepo) commit(name, contents string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(contents), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", name)
	r.git("commit", "-qm", name+": "+contents)
}

func (r *mergeRepo) runner() *Runner {
	return NewRunner(filepath.Join(r.dir, ".git"), false)
}

// TestBranchMergeStatus covers each way work reaches main: a true merge, a
// multi-commit squash merge, a fast-forwarded rebase, and work that never
// landed.
func TestBranchMergeStatus(t *testing.T) {
	r := newMergeRepo(t)

	// A branch merged with a real merge commit.
	r.git("checkout", "-qb", "true-merge")
	r.commit("true.txt", "one\n")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--no-ff", "-m", "merge true-merge", "true-merge")

	// A multi-commit branch squash-merged into main. No individual commit of
	// the branch appears in main, so only the cumulative-tree probe finds it.
	r.git("checkout", "-qb", "squashed", "main")
	r.commit("squash.txt", "one\n")
	r.commit("squash.txt", "one\ntwo\n")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--squash", "squashed")
	r.git("commit", "-qm", "squashed everything")

	// A multi-commit branch rebase-merged and fast-forwarded, so it is also a
	// plain ancestor and the cheapest check wins.
	r.git("checkout", "-qb", "rebased", "main")
	r.commit("rebase.txt", "one\n")
	r.commit("rebase.txt", "one\ntwo\n")
	r.git("checkout", "-q", "main")
	r.commit("unrelated.txt", "moved main forward\n")
	r.git("checkout", "-q", "rebased")
	r.git("rebase", "-q", "main")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--ff-only", "rebased")

	// Real unmerged work.
	r.git("checkout", "-qb", "pending", "main")
	r.commit("pending.txt", "not landed\n")
	r.git("checkout", "-q", "main")

	runner := r.runner()
	ctx := context.Background()

	tests := []struct {
		branch     string
		wantMerged bool
		wantMethod MergeMethod
	}{
		{"true-merge", true, MergeAncestor},
		{"squashed", true, MergeSquash},
		{"rebased", true, MergeAncestor},
		{"pending", false, MergeNone},
	}

	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			got, err := runner.BranchMergeStatus(ctx, tt.branch, "main")
			if err != nil {
				t.Fatalf("BranchMergeStatus(%s): %v", tt.branch, err)
			}
			if got.Merged != tt.wantMerged || got.Method != tt.wantMethod {
				t.Errorf("BranchMergeStatus(%s) = {%v %q}, want {%v %q}",
					tt.branch, got.Merged, got.Method, tt.wantMerged, tt.wantMethod)
			}
		})
	}
}

// TestBranchMergeStatusRebaseNotFastForwarded covers the case the plain
// ancestor test misses: a rebase-merged branch whose target has moved on, so
// the branch tip is no longer an ancestor of main.
func TestBranchMergeStatusRebaseNotFastForwarded(t *testing.T) {
	r := newMergeRepo(t)

	r.git("checkout", "-qb", "feature")
	r.commit("feature.txt", "one\n")
	r.commit("feature.txt", "one\ntwo\n")

	// Replay the same commits onto main, leaving the original branch tip
	// behind: what a "Rebase and merge" produces once main moves on.
	r.git("checkout", "-q", "main")
	r.commit("other.txt", "landed first\n")
	r.git("cherry-pick", "feature~1", "feature")
	r.commit("after.txt", "main moved on\n")

	got, err := r.runner().BranchMergeStatus(context.Background(), "feature", "main")
	if err != nil {
		t.Fatalf("BranchMergeStatus: %v", err)
	}
	if !got.Merged || got.Method != MergeRebase {
		t.Errorf("BranchMergeStatus(feature) = {%v %q}, want {true %q}", got.Merged, got.Method, MergeRebase)
	}
}

// TestBranchMergeStatusSingleCommitIsEquivalent pins the ambiguous case: with
// one commit, a squash merge and a rebase merge leave identical evidence, so
// neither label may be claimed.
func TestBranchMergeStatusSingleCommitIsEquivalent(t *testing.T) {
	r := newMergeRepo(t)

	r.git("checkout", "-qb", "feature")
	r.commit("feature.txt", "only\n")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--squash", "feature")
	r.git("commit", "-qm", "squashed the one commit")
	r.commit("after.txt", "main moved on\n")

	got, err := r.runner().BranchMergeStatus(context.Background(), "feature", "main")
	if err != nil {
		t.Fatalf("BranchMergeStatus: %v", err)
	}
	if !got.Merged || got.Method != MergeEquivalent {
		t.Errorf("BranchMergeStatus(feature) = {%v %q}, want {true %q}", got.Merged, got.Method, MergeEquivalent)
	}
}

// TestBranchMergeStatusWithoutGitIdentity covers CI and fresh containers, where
// git has no configured user: the checks must not need one.
func TestBranchMergeStatusWithoutGitIdentity(t *testing.T) {
	r := newMergeRepo(t)

	r.git("checkout", "-qb", "squashed")
	r.commit("squash.txt", "one\n")
	r.commit("squash.txt", "one\ntwo\n")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--squash", "squashed")
	r.git("commit", "-qm", "squashed everything")

	// Strip every source of a git identity from the environment the Runner
	// inherits: no global or system config, no HOME to find one in.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "")
	t.Setenv("GIT_AUTHOR_EMAIL", "")
	t.Setenv("GIT_COMMITTER_NAME", "")
	t.Setenv("GIT_COMMITTER_EMAIL", "")

	got, err := r.runner().BranchMergeStatus(context.Background(), "squashed", "main")
	if err != nil {
		t.Fatalf("BranchMergeStatus: %v", err)
	}
	if !got.Merged || got.Method != MergeSquash {
		t.Errorf("BranchMergeStatus(squashed) = {%v %q}, want {true %q}", got.Merged, got.Method, MergeSquash)
	}
}

// commitLogo commits binary contents to logo.png. commit cannot, since it puts
// the contents in the commit message.
func (r *mergeRepo) commitLogo(contents string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, "logo.png"), []byte(contents), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", "logo.png")
	r.git("commit", "-qm", "update logo")
}

// TestBranchMergeStatusBinaryChanges checks that two different changes to one
// binary file are not mistaken for each other. A text diff of either reads
// only "Binary files differ", so their patch-ids must come from the content.
func TestBranchMergeStatusBinaryChanges(t *testing.T) {
	r := newMergeRepo(t)
	r.commitLogo("\x00\x01original")

	r.git("checkout", "-qb", "different")
	r.commitLogo("\x00\x01branch")
	r.git("checkout", "-qb", "squashed", "main")
	r.commitLogo("\x00\x01landed")
	r.commitLogo("\x00\x01landed twice")

	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--squash", "squashed")
	r.git("commit", "-qm", "squashed the binary change")

	statuses, errs := r.runner().BranchMergeStatuses(context.Background(), []string{"different", "squashed"}, "main")
	want := []MergeStatus{{Merged: false, Method: MergeNone}, {Merged: true, Method: MergeSquash}}
	for i, branch := range []string{"different", "squashed"} {
		if errs[i] != nil {
			t.Fatalf("BranchMergeStatuses(%s): %v", branch, errs[i])
		}
		if statuses[i] != want[i] {
			t.Errorf("BranchMergeStatuses(%s) = %+v, want %+v", branch, statuses[i], want[i])
		}
	}
}

// TestBranchMergeStatusesComparesEachBranchWithItsOwnRange checks that the
// shared patch-ids do not let one branch match target commits that only
// another branch lacks. main added a fix and reverted it; reapply forked after
// both and reapplies the fix. Its commit matches main's fix, but that fix is
// already in reapply's own history, so the reapplied work has not landed.
// early forked before the fix, so the fix is among the commits it lacks.
func TestBranchMergeStatusesComparesEachBranchWithItsOwnRange(t *testing.T) {
	r := newMergeRepo(t)

	r.git("checkout", "-qb", "early", "main")
	r.commit("early.txt", "early\n")

	r.git("checkout", "-q", "main")
	r.commit("fix.txt", "fix\n")
	r.git("rm", "-q", "fix.txt")
	r.git("commit", "-qm", "revert fix")

	r.git("checkout", "-qb", "reapply", "main")
	r.commit("fix.txt", "fix\n")

	statuses, errs := r.runner().BranchMergeStatuses(context.Background(), []string{"early", "reapply"}, "main")
	for i, branch := range []string{"early", "reapply"} {
		if errs[i] != nil {
			t.Fatalf("BranchMergeStatuses(%s): %v", branch, errs[i])
		}
		if statuses[i].Merged {
			t.Errorf("BranchMergeStatuses(%s) = %+v, want unmerged", branch, statuses[i])
		}
	}
}

// TestBranchMergeStatusesMatchesSingleChecks checks that one batched call
// gives each branch the answer a call for that branch alone would.
func TestBranchMergeStatusesMatchesSingleChecks(t *testing.T) {
	r := newMergeRepo(t)

	r.git("checkout", "-qb", "squashed")
	r.commit("squash.txt", "one\n")
	r.commit("squash.txt", "one\ntwo\n")
	r.git("checkout", "-qb", "rebased", "main")
	r.commit("rebase.txt", "one\n")
	r.commit("rebase.txt", "one\ntwo\n")
	r.git("checkout", "-qb", "pending", "main")
	r.commit("pending.txt", "not landed\n")
	r.git("checkout", "-qb", "merged", "main")
	r.commit("merged.txt", "one\n")

	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--squash", "squashed")
	r.git("commit", "-qm", "squashed everything")
	r.git("cherry-pick", "rebased~1", "rebased")
	r.git("merge", "-q", "--no-ff", "-m", "merge merged", "merged")

	runner := r.runner()
	ctx := context.Background()
	branches := []string{"squashed", "rebased", "pending", "merged"}
	statuses, errs := runner.BranchMergeStatuses(ctx, branches, "main")

	want := []MergeMethod{MergeSquash, MergeRebase, MergeNone, MergeAncestor}
	for i, branch := range branches {
		if errs[i] != nil {
			t.Fatalf("BranchMergeStatuses(%s): %v", branch, errs[i])
		}
		single, err := runner.BranchMergeStatus(ctx, branch, "main")
		if err != nil {
			t.Fatalf("BranchMergeStatus(%s): %v", branch, err)
		}
		if statuses[i].Method != want[i] || statuses[i] != single {
			t.Errorf("%s: batched %+v, single %+v, want method %q", branch, statuses[i], single, want[i])
		}
	}
}

// TestBranchMergeStatusUnknownBranch checks that a branch git cannot resolve is
// reported as an error rather than as unmerged.
func TestBranchMergeStatusUnknownBranch(t *testing.T) {
	r := newMergeRepo(t)
	if _, err := r.runner().BranchMergeStatus(context.Background(), "no-such-branch", "main"); err == nil {
		t.Error("BranchMergeStatus(no-such-branch): expected error, got nil")
	}
}
