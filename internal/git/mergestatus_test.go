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

func TestParseCherryEquivalent(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      bool
		wantCount int
	}{
		{"all equivalent", "- abc123\n- def456\n", true, 2},
		{"single equivalent", "- abc123", true, 1},
		{"mixed", "- abc123\n+ def456\n", false, 0},
		{"none equivalent", "+ abc123\n+ def456\n", false, 0},
		{"empty", "", false, 0},
		{"whitespace only", "\n  \n", false, 0},
		{"blank lines between", "\n- abc123\n\n- def456\n\n", true, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, count := parseCherryEquivalent(tt.input)
			if got != tt.want || count != tt.wantCount {
				t.Errorf("parseCherryEquivalent(%q) = (%v, %d), want (%v, %d)",
					tt.input, got, count, tt.want, tt.wantCount)
			}
		})
	}
}

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

// probeSHA recomputes the synthetic commit isSquashMerged builds for a branch.
// probeEnv fixes the identity and the dates, so the SHA is deterministic and
// the test can look for that exact object in the repo afterwards.
func (r *mergeRepo) probeSHA(branch, target string) string {
	r.t.Helper()

	runner := r.runner()
	ctx := context.Background()

	base, err := runner.Query(ctx, "merge-base", target, branch)
	if err != nil {
		r.t.Fatal(err)
	}
	tree, err := runner.Query(ctx, "rev-parse", branch+"^{tree}")
	if err != nil {
		r.t.Fatal(err)
	}

	sha, err := runner.queryWithEnv(ctx, probeEnv(r.t.TempDir(), runner.GitDir),
		"commit-tree", tree, "-p", base, "-m", "wt-merge-probe")
	if err != nil {
		r.t.Fatal(err)
	}
	return sha
}

// hasObject reports whether the repository itself contains an object, ignoring
// any throwaway object store.
func (r *mergeRepo) hasObject(sha string) bool {
	r.t.Helper()
	_, err := r.runner().Query(context.Background(), "cat-file", "-e", sha)
	return err == nil
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

// TestBranchMergeStatusDoesNotWriteToRepo guards the dry-run contract: the
// squash probe needs a commit object, and it must not land in the user's repo.
func TestBranchMergeStatusDoesNotWriteToRepo(t *testing.T) {
	r := newMergeRepo(t)

	r.git("checkout", "-qb", "squashed")
	r.commit("squash.txt", "one\n")
	r.commit("squash.txt", "one\ntwo\n")
	r.git("checkout", "-q", "main")
	r.git("merge", "-q", "--squash", "squashed")
	r.git("commit", "-qm", "squashed everything")

	// The probe object is what must never land in the repo. Counting object
	// files cannot tell an addition from git repacking behind our back, so look
	// for the exact commit the probe builds.
	probe := r.probeSHA("squashed", "main")
	if r.hasObject(probe) {
		t.Fatalf("probe commit %s was already in the repo before the check ran", probe)
	}

	got, err := r.runner().BranchMergeStatus(context.Background(), "squashed", "main")
	if err != nil {
		t.Fatalf("BranchMergeStatus: %v", err)
	}
	if got.Method != MergeSquash {
		t.Fatalf("BranchMergeStatus = %q, want %q (the probe must have run)", got.Method, MergeSquash)
	}

	if r.hasObject(probe) {
		t.Errorf("probe commit %s was written into the repo; it belongs in the throwaway object store", probe)
	}
}

// TestBranchMergeStatusWithoutGitIdentity covers CI and fresh containers, where
// git has no configured user: the synthetic probe commit must supply its own.
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
