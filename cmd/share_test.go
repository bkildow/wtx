package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
)

func TestResolveSharePath(t *testing.T) {
	root := ui.CanonicalPath(t.TempDir())
	worktrees := filepath.Join(root, ".worktrees")
	managed := managedWithin(root, []string{worktrees, filepath.Join(worktrees, "shared"), filepath.Join(root, ".worktrees", "bin")})
	elsewhere := t.TempDir()

	tests := []struct {
		name, cwd, arg string
		want           string
		wantErr        bool
	}{
		{"relative to root from outside", elsewhere, ".env", ".env", false},
		{"relative to cwd inside root", filepath.Join(root, "app"), ".env", filepath.Join("app", ".env"), false},
		{"relative to root from a nested worktree", filepath.Join(worktrees, "feature"), ".env", ".env", false},
		{"absolute inside", elsewhere, filepath.Join(root, "config", "local.yml"), filepath.Join("config", "local.yml"), false},
		{"absolute outside", elsewhere, filepath.Join(elsewhere, ".env"), "", true},
		{"escapes the root", root, "../x", "", true},
		{"the root itself", root, ".", "", true},
		{"git data", root, ".git/config", "", true},
		{"project config", root, ".worktree.yml", "", true},
		{"worktrees dir", root, ".worktrees", "", true},
		{"inside the shared dir", root, ".worktrees/shared/symlink/.env", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveSharePath(root, tt.cwd, tt.arg, managed)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("resolveSharePath = %q, %v; want %q, wantErr %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestResolveSharePathBareWorktree(t *testing.T) {
	root := ui.CanonicalPath(t.TempDir())
	worktrees := filepath.Join(root, "worktrees")
	main := filepath.Join(worktrees, "main")
	managed := managedWithin(main, []string{worktrees, filepath.Join(root, "shared"), filepath.Join(root, "bin")})
	if len(managed) != 0 {
		t.Fatalf("managedWithin(main) = %v; the worktrees dir holds main, the others lie outside it", managed)
	}

	got, err := resolveSharePath(main, main, ".env", managed)
	if err != nil || got != ".env" {
		t.Errorf("resolveSharePath = %q, %v; want .env", got, err)
	}
}

func TestShareSource(t *testing.T) {
	root := ui.CanonicalPath(t.TempDir())
	main := git.WorktreeInfo{Path: filepath.Join(root, "worktrees", "main"), Branch: "main"}
	feature := git.WorktreeInfo{Path: filepath.Join(root, "worktrees", "feature"), Branch: "feature"}
	wts := project.Worktrees{Managed: []git.WorktreeInfo{main, feature}, Main: git.WorktreeInfo{Path: root, Branch: "main"}, HasMain: true}

	if got, err := shareSource(false, root, wts, feature.Path); err != nil || got != root {
		t.Errorf("checkout source = %v, %v; want the root", got, err)
	}
	if got, err := shareSource(true, root, wts, filepath.Join(feature.Path, "sub")); err != nil || got != feature.Path {
		t.Errorf("bare source = %v, %v; want feature", got, err)
	}
	if _, err := shareSource(true, root, wts, root); err == nil {
		t.Error("bare source outside a worktree: want an error")
	}
}

func TestShareCandidates(t *testing.T) {
	root := ui.CanonicalPath(t.TempDir())
	for _, dir := range []string{"node_modules", ".worktrees"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, ".linked")); err != nil {
		t.Fatal(err)
	}
	untracked := []string{".env", ".linked", ".worktrees/", "node_modules/", "src/new.go", "gone"}

	got := shareCandidates(root, untracked, managedWithin(root, []string{filepath.Join(root, ".worktrees")}))
	if want := []string{".env", "node_modules"}; !reflect.DeepEqual(got, want) {
		t.Errorf("shareCandidates = %v, want %v", got, want)
	}
}

func TestSharedElsewhere(t *testing.T) {
	shared := t.TempDir()
	if err := os.MkdirAll(filepath.Join(shared, "symlink"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "symlink", ".env"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sharedElsewhere(shared, ".env", shareCopy); err == nil {
		t.Error("copy of a symlink-shared path: want an error")
	}
	if err := sharedElsewhere(shared, ".env", shareSymlink); err != nil {
		t.Errorf("symlink of a symlink-shared path: %v (LinkBack decides)", err)
	}
}
