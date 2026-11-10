package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
)

func TestRootApplyError(t *testing.T) {
	root := filepath.FromSlash("/proj")
	rootMain := git.WorktreeInfo{Path: root, Branch: "main"}
	feature := git.WorktreeInfo{Path: filepath.FromSlash("/elsewhere/feature"), Branch: "feature"}
	mainWT := git.WorktreeInfo{Path: filepath.Join(root, "worktrees", "main"), Branch: "main"}
	checkout := project.ClassifyWorktrees([]git.WorktreeInfo{rootMain, feature}, root, project.CheckoutLayout, "main")
	detached := project.ClassifyWorktrees([]git.WorktreeInfo{{Path: root}, feature}, root, project.CheckoutLayout, "main")
	bare := project.ClassifyWorktrees([]git.WorktreeInfo{mainWT, feature}, root, project.BareLayout, "main")

	tests := []struct {
		name    string
		layout  project.LayoutKind
		wts     project.Worktrees
		args    []string
		wantErr bool
	}{
		{"checkout: the root's branch", project.CheckoutLayout, checkout, []string{"main"}, true},
		{"in-repo: the root's branch", project.InRepoLayout, checkout, []string{"main"}, true},
		{"checkout: a Managed worktree", project.CheckoutLayout, checkout, []string{"feature"}, false},
		{"checkout: no argument", project.CheckoutLayout, checkout, nil, false},
		{"checkout: detached root", project.CheckoutLayout, detached, []string{""}, false},
		{"bare: main is Managed", project.BareLayout, bare, []string{"main"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rootApplyError(tt.layout, tt.wts, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("rootApplyError = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestApplyCopyMode(t *testing.T) {
	copyDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(copyDir, ".aws"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		flags   map[string][]string
		want    project.CopyMode
		wantErr bool
	}{
		{"default seeds", nil, project.KeepExisting, false},
		{"--force replaces", map[string][]string{"force": {"true"}}, project.ReplaceExisting, false},
		{"--overwrite names a path", map[string][]string{"overwrite": {"./.aws"}}, project.CopyMode{Seed: true, Replace: []string{".aws"}}, false},
		{"--overwrite outside shared/copy", map[string][]string{"overwrite": {"vendor"}}, project.CopyMode{}, true},
		{"--overwrite escaping the worktree", map[string][]string{"overwrite": {"../.aws"}}, project.CopyMode{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newApplyCmd()
			for name, values := range tt.flags {
				for _, v := range values {
					if err := cmd.Flags().Set(name, v); err != nil {
						t.Fatal(err)
					}
				}
			}
			got, err := applyCopyMode(cmd, copyDir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("applyCopyMode err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("applyCopyMode = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestApplyForceAndOverwriteExclusive(t *testing.T) {
	cmd := newApplyCmd()
	_ = cmd.Flags().Set("force", "true")
	_ = cmd.Flags().Set("overwrite", ".aws")
	if err := cmd.ValidateFlagGroups(); err == nil {
		t.Error("--force with --overwrite was accepted")
	}
}
