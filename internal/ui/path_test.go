package ui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDisplayPath(t *testing.T) {
	// Canonicalize the temp dir up front so expected absolute paths match
	// (on macOS t.TempDir lives under the /var -> /private/var symlink).
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(tmp, "home")
	root := filepath.Join(tmp, "a", "b")
	for _, d := range []string{home, root, filepath.Join(root, "worktrees", "feat"), filepath.Join(tmp, "a", "bc")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)

	link := filepath.Join(tmp, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		root string
		path string
		want string
	}{
		{"inside root", root, filepath.Join(root, "worktrees", "feat"), filepath.Join("worktrees", "feat")},
		{"inside root missing path", root, filepath.Join(root, "worktrees", "gone"), filepath.Join("worktrees", "gone")},
		{"root itself", root, root, "."},
		{"sibling with shared prefix", root, filepath.Join(tmp, "a", "bc"), filepath.Join(tmp, "a", "bc")},
		{"parent of root", root, filepath.Join(tmp, "a"), filepath.Join(tmp, "a")},
		{"under home", root, filepath.Join(home, ".wtx", "proj", "worktrees", "feat"), filepath.Join("~", ".wtx", "proj", "worktrees", "feat")},
		{"home itself", root, home, "~"},
		{"home prefix without separator", root, home + "x", home + "x"},
		{"elsewhere", root, filepath.Join(tmp, "other", "wt"), filepath.Join(tmp, "other", "wt")},
		{"empty root uses home", "", filepath.Join(home, "proj"), filepath.Join("~", "proj")},
		{"empty path", root, "", ""},
		{"root via symlink", link, filepath.Join(root, "worktrees", "feat"), filepath.Join("worktrees", "feat")},
		{"path via symlink", root, filepath.Join(link, "worktrees", "feat"), filepath.Join("worktrees", "feat")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DisplayPath(tt.root, tt.path); got != tt.want {
				t.Errorf("DisplayPath(%q, %q) = %q, want %q", tt.root, tt.path, got, tt.want)
			}
		})
	}
}
