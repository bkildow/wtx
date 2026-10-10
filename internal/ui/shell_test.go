package ui

import "testing"

func TestShellQuote(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"safe", "feature/a-b_c.d:e@f+g=h,i", "feature/a-b_c.d:e@f+g=h,i"},
		{"empty", "", "''"},
		{"spaces", "my dir", "'my dir'"},
		{"dollar", "$HOME/x", "'$HOME/x'"},
		{"backtick", "a`id`b", "'a`id`b'"},
		{"backslash", `a\b`, `'a\b'`},
		{"single quote", "it's", `'it'\''s'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShellQuote(tt.in); got != tt.want {
				t.Errorf("ShellQuote(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}
