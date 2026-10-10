package cmd

import "testing"

func TestBranchDeleteCommand(t *testing.T) {
	tests := []struct{ gitDir, branch, want string }{
		{"/p/.bare", "feature/x", "git --git-dir /p/.bare branch -D feature/x"},
		{"/my p/.bare", "it's", `git --git-dir '/my p/.bare' branch -D 'it'\''s'`},
	}
	for _, tt := range tests {
		if got := branchDeleteCommand(tt.gitDir, tt.branch); got != tt.want {
			t.Errorf("branchDeleteCommand(%q, %q) = %s, want %s", tt.gitDir, tt.branch, got, tt.want)
		}
	}
}
