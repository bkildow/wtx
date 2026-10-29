package cmd

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bkildow/wtx/internal/forge"
	"github.com/bkildow/wtx/internal/git"
)

// TestPRMerged pins the guards that stop a merged PR from condemning a
// worktree that is still live: gh matches PRs by head branch name alone.
func TestPRMerged(t *testing.T) {
	const head = "abc123"
	worktree := git.WorktreeInfo{Branch: "feature", Head: head}

	tests := []struct {
		name string
		pr   *forge.PullRequest
		want bool
	}{
		{
			"merged into the default branch at this tip",
			&forge.PullRequest{Number: 1, State: forge.StateMerged, HeadOID: head, BaseRef: "main"},
			true,
		},
		{"no pull request", nil, false},
		{
			"still open",
			&forge.PullRequest{Number: 1, State: forge.StateOpen, HeadOID: head, BaseRef: "main"},
			false,
		},
		{
			"closed without merging",
			&forge.PullRequest{Number: 1, State: forge.StateClosed, HeadOID: head, BaseRef: "main"},
			false,
		},
		{
			"merged into some other base",
			&forge.PullRequest{Number: 1, State: forge.StateMerged, HeadOID: head, BaseRef: "release/2.0"},
			false,
		},
		{
			"branch name reused, or commits pushed after the merge",
			&forge.PullRequest{Number: 1, State: forge.StateMerged, HeadOID: "999fff", BaseRef: "main"},
			false,
		},
		{
			"forge reported no head commit",
			&forge.PullRequest{Number: 1, State: forge.StateMerged, HeadOID: "", BaseRef: "main"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The worktree carries its head, so no git call is needed.
			if got := prMerged(context.Background(), nil, tt.pr, worktree, "main"); got != tt.want {
				t.Errorf("prMerged = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeReason(t *testing.T) {
	tests := []struct {
		method git.MergeMethod
		want   string
	}{
		{git.MergeAncestor, "merged"},
		{git.MergeRebase, "merged (rebase)"},
		{git.MergeSquash, "merged (squash)"},
		{git.MergeEquivalent, "merged (patch-equivalent)"},
		{git.MergeNone, "merged"},
	}

	for _, tt := range tests {
		t.Run(string(tt.method), func(t *testing.T) {
			if got := mergeReason(tt.method); got != tt.want {
				t.Errorf("mergeReason(%q) = %q, want %q", tt.method, got, tt.want)
			}
		})
	}
}

// TestPartitionPrunable pins the --force contract: dirty merged worktrees are
// kept unless forced, clean ones are always removed.
func TestPartitionPrunable(t *testing.T) {
	clean := prunable{worktree: git.WorktreeInfo{Branch: "clean"}}
	dirty := prunable{worktree: git.WorktreeInfo{Branch: "dirty"}, dirty: true}
	candidates := []prunable{clean, dirty}

	tests := []struct {
		name       string
		force      bool
		wantRemove []string
		wantKeep   []string
	}{
		{"without --force dirty is kept", false, []string{"clean"}, []string{"dirty"}},
		{"with --force everything goes", true, []string{"clean", "dirty"}, nil},
	}

	branches := func(ps []prunable) []string {
		var out []string
		for _, p := range ps {
			out = append(out, p.worktree.Branch)
		}
		return out
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			remove, keep := partitionPrunable(candidates, tt.force)
			if got := branches(remove); !slices.Equal(got, tt.wantRemove) {
				t.Errorf("remove = %v, want %v", got, tt.wantRemove)
			}
			if got := branches(keep); !slices.Equal(got, tt.wantKeep) {
				t.Errorf("keep = %v, want %v", got, tt.wantKeep)
			}
		})
	}
}

func TestPrunableStatus(t *testing.T) {
	if got := (prunable{}).status(); got != "clean" {
		t.Errorf("status() = %q, want clean", got)
	}
	if got := (prunable{dirty: true}).status(); got != "dirty" {
		t.Errorf("status() = %q, want dirty", got)
	}
}

// stubChecker answers merge checks from a fixed set of merged branches. Each
// call sleeps for the branch's delay, so checks finish out of order.
type stubChecker struct {
	merged map[string]bool
	delay  map[string]time.Duration
}

func (s stubChecker) BranchMergeStatus(_ context.Context, branch, _ string) (git.MergeStatus, error) {
	time.Sleep(s.delay[branch])
	if s.merged[branch] {
		return git.MergeStatus{Merged: true, Method: git.MergeAncestor}, nil
	}
	return git.MergeStatus{}, nil
}

func (s stubChecker) IsWorktreeDirty(context.Context, string) (bool, error) {
	return false, nil
}

func (s stubChecker) Query(context.Context, ...string) (string, error) {
	return "", errors.New("unexpected query")
}

// fakeForge returns prs and err for every lookup, and records the branches
// it was asked about.
type fakeForge struct {
	prs   map[string]*forge.PullRequest
	err   error
	calls [][]string
}

func (f *fakeForge) PRsForBranches(_ context.Context, branches []string) (map[string]*forge.PullRequest, error) {
	f.calls = append(f.calls, branches)
	return f.prs, f.err
}

func worktreesFor(branches ...string) []git.WorktreeInfo {
	var wts []git.WorktreeInfo
	for _, b := range branches {
		wts = append(wts, git.WorktreeInfo{Branch: b, Path: "/wt/" + b, Head: "head-" + b})
	}
	return wts
}

// TestFindPrunableKeepsWorktreeOrder checks that the concurrent checks report
// candidates in worktree order even when later checks finish first.
func TestFindPrunableKeepsWorktreeOrder(t *testing.T) {
	captureUI(t)
	g := stubChecker{
		merged: map[string]bool{"a": true, "c": true, "d": true},
		delay:  map[string]time.Duration{"a": 30 * time.Millisecond, "c": 15 * time.Millisecond},
	}

	got := findPrunable(context.Background(), g, nil, worktreesFor("a", "b", "c", "d"), "main")

	var branches []string
	for _, p := range got {
		branches = append(branches, p.worktree.Branch)
	}
	if want := []string{"a", "c", "d"}; !slices.Equal(branches, want) {
		t.Errorf("candidates = %v, want %v", branches, want)
	}
}

// TestFindPrunableLooksUpUnmergedBranchesOnce checks that only the branches
// git leaves unmerged are looked up, all in one call, and that a merged pull
// request at the worktree's head marks it merged.
func TestFindPrunableLooksUpUnmergedBranchesOnce(t *testing.T) {
	captureUI(t)
	f := &fakeForge{prs: map[string]*forge.PullRequest{
		"b": {Number: 12, State: forge.StateMerged, HeadOID: "head-b", BaseRef: "main"},
		"c": {Number: 13, State: forge.StateOpen, HeadOID: "head-c", BaseRef: "main"},
	}}
	g := stubChecker{merged: map[string]bool{"a": true}}

	got := findPrunable(context.Background(), g, f, worktreesFor("a", "b", "c"), "main")

	if want := [][]string{{"b", "c"}}; !slices.EqualFunc(f.calls, want, slices.Equal) {
		t.Errorf("lookups = %v, want %v", f.calls, want)
	}
	var reasons []string
	for _, p := range got {
		reasons = append(reasons, p.worktree.Branch+": "+p.reason)
	}
	if want := []string{"a: merged", "b: merged (PR #12)"}; !slices.Equal(reasons, want) {
		t.Errorf("candidates = %v, want %v", reasons, want)
	}
}

// TestFindPrunableWarnsOnceWhenLookupFails checks that a gh failure is
// reported once and leaves every branch to git's verdict.
func TestFindPrunableWarnsOnceWhenLookupFails(t *testing.T) {
	buf := captureUI(t)
	f := &fakeForge{err: errors.New("gh: not logged in")}
	branches := make([]string, 3*checkWorkers)
	for i := range branches {
		branches[i] = string(rune('a' + i))
	}

	got := findPrunable(context.Background(), stubChecker{}, f, worktreesFor(branches...), "main")

	if len(got) != 0 {
		t.Errorf("got %d candidates, want none", len(got))
	}
	if n := strings.Count(buf.String(), "Could not check pull request state"); n != 1 {
		t.Errorf("lookup warning printed %d times, want 1:\n%s", n, buf.String())
	}
	if len(f.calls) != 1 {
		t.Errorf("gh called %d times, want 1", len(f.calls))
	}
}

// TestRemoveConcurrently checks that a failed removal is reported and marked,
// so its branch is not deleted, while the rest go ahead.
func TestRemoveConcurrently(t *testing.T) {
	buf := captureUI(t)
	var items []prunable
	for _, wt := range worktreesFor("a", "b", "c", "d", "e") {
		items = append(items, prunable{worktree: wt})
	}

	ok := removeConcurrently(items, 2, func(wt git.WorktreeInfo) error {
		if wt.Branch == "c" {
			return errors.New("locked")
		}
		return nil
	})

	want := []bool{true, true, false, true, true}
	for i := range want {
		if ok[i] != want[i] {
			t.Errorf("ok[%d] (%s) = %v, want %v", i, items[i].worktree.Branch, ok[i], want[i])
		}
	}
	if !strings.Contains(buf.String(), "Could not remove worktree c: locked") {
		t.Errorf("missing failure warning:\n%s", buf.String())
	}
}

// TestFindPrunableKeepsPartialLookup checks that pull requests found before a
// lookup failed still count.
func TestFindPrunableKeepsPartialLookup(t *testing.T) {
	captureUI(t)
	f := &fakeForge{
		prs: map[string]*forge.PullRequest{"a": {Number: 5, State: forge.StateMerged, HeadOID: "head-a", BaseRef: "main"}},
		err: errors.New("gh: rate limited"),
	}

	got := findPrunable(context.Background(), stubChecker{}, f, worktreesFor("a", "b"), "main")

	if len(got) != 1 || got[0].reason != "merged (PR #5)" {
		t.Errorf("candidates = %+v, want only a merged by PR #5", got)
	}
}
