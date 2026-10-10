package project

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bkildow/wtx/internal/git"
)

func TestClassifyWorktrees(t *testing.T) {
	root := filepath.FromSlash("/proj")
	bare := git.WorktreeInfo{Path: filepath.Join(root, ".bare"), Bare: true}
	rootWT := git.WorktreeInfo{Path: root, Branch: "main", Head: "abc"}
	mainWT := git.WorktreeInfo{Path: filepath.Join(root, "worktrees", "main"), Branch: "main"}
	feature := git.WorktreeInfo{Path: filepath.FromSlash("/elsewhere/feature"), Branch: "feature"}
	develop := git.WorktreeInfo{Path: filepath.Join(root, "worktrees", "develop"), Branch: "develop"}

	tests := []struct {
		name       string
		all        []git.WorktreeInfo
		layout     LayoutKind
		mainBranch string
		want       Worktrees
	}{
		{
			name:       "bare: Main is the Managed worktree on the main branch",
			all:        []git.WorktreeInfo{bare, mainWT, feature},
			layout:     BareLayout,
			mainBranch: "main",
			want:       Worktrees{Managed: []git.WorktreeInfo{mainWT, feature}, Main: mainWT, HasMain: true},
		},
		{
			name:       "bare: main_branch picks the Main worktree",
			all:        []git.WorktreeInfo{bare, mainWT, develop},
			layout:     BareLayout,
			mainBranch: "develop",
			want:       Worktrees{Managed: []git.WorktreeInfo{mainWT, develop}, Main: develop, HasMain: true},
		},
		{
			name:       "bare: no worktree on the main branch",
			all:        []git.WorktreeInfo{bare, feature},
			layout:     BareLayout,
			mainBranch: "main",
			want:       Worktrees{Managed: []git.WorktreeInfo{feature}},
		},
		{
			name:       "checkout: Main is the Project root with its Branch",
			all:        []git.WorktreeInfo{rootWT, feature},
			layout:     CheckoutLayout,
			mainBranch: "main",
			want:       Worktrees{Managed: []git.WorktreeInfo{feature}, Main: rootWT, HasMain: true},
		},
		{
			name:       "in-repo: a linked worktree on the main branch stays Managed",
			all:        []git.WorktreeInfo{rootWT, mainWT},
			layout:     InRepoLayout,
			mainBranch: "main",
			want:       Worktrees{Managed: []git.WorktreeInfo{mainWT}, Main: rootWT, HasMain: true},
		},
		{
			name:       "checkout: Project root missing from the listing is still Main",
			all:        []git.WorktreeInfo{feature},
			layout:     CheckoutLayout,
			mainBranch: "main",
			want:       Worktrees{Managed: []git.WorktreeInfo{feature}, Main: git.WorktreeInfo{Path: root}, HasMain: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyWorktrees(tt.all, root, tt.layout, tt.mainBranch)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ClassifyWorktrees =\n %+v\nwant\n %+v", got, tt.want)
			}
		})
	}
}
