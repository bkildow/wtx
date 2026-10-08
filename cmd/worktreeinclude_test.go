package cmd

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestDropManagedPaths(t *testing.T) {
	root := filepath.FromSlash("/proj")
	files := []string{".env", ".worktrees/shared/copy/.env", ".worktrees/bin/x", "app/.env", ".worktreesx/.env"}

	tests := []struct {
		name    string
		src     string
		managed []string
		want    []string
	}{
		{
			name:    "init layout drops wtx dirs under the main worktree",
			src:     root,
			managed: []string{filepath.Join(root, ".worktrees"), filepath.Join(root, ".worktrees", "shared")},
			want:    []string{".env", "app/.env", ".worktreesx/.env"},
		},
		{
			name:    "bare layout dirs outside the main worktree are ignored",
			src:     filepath.Join(root, "worktrees", "main"),
			managed: []string{filepath.Join(root, "worktrees"), filepath.Join(root, "shared")},
			want:    files,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dropManagedPaths(files, tt.src, tt.managed); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("dropManagedPaths = %v, want %v", got, tt.want)
			}
		})
	}
}
