package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bkildow/wtx/internal/config"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestApplyInclude(t *testing.T) {
	src := t.TempDir()
	wt := t.TempDir()
	writeTestFile(t, filepath.Join(src, ".env"), "SECRET=1")
	writeTestFile(t, filepath.Join(src, "secrets", "key.txt"), "key")
	writeTestFile(t, filepath.Join(src, "unlisted"), "nope")
	writeTestFile(t, filepath.Join(wt, ".env"), "stale")

	include := &IncludeSource{Dir: src, Files: []string{".env", "secrets/key.txt", "gone.txt"}}
	n, err := ApplyInclude(include, wt, false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2 (missing source files are skipped)", n)
	}
	if got := readTestFile(t, filepath.Join(wt, ".env")); got != "SECRET=1" {
		t.Errorf(".env = %q, want overwritten with source", got)
	}
	if got := readTestFile(t, filepath.Join(wt, "secrets", "key.txt")); got != "key" {
		t.Errorf("secrets/key.txt = %q", got)
	}
	if _, err := os.Stat(filepath.Join(wt, "unlisted")); !os.IsNotExist(err) {
		t.Error("unlisted file should not be copied")
	}
}

func TestApplyIncludeDryRun(t *testing.T) {
	src := t.TempDir()
	wt := t.TempDir()
	writeTestFile(t, filepath.Join(src, ".env"), "SECRET=1")

	n, err := ApplyInclude(&IncludeSource{Dir: src, Files: []string{".env"}}, wt, true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(wt, ".env")); !os.IsNotExist(err) {
		t.Error("dry-run should not copy files")
	}
}

func TestApplyIncludeSkips(t *testing.T) {
	src := t.TempDir()
	writeTestFile(t, filepath.Join(src, ".env"), "SECRET=1")

	tests := []struct {
		name    string
		include *IncludeSource
		dest    string
	}{
		{"nil source", nil, t.TempDir()},
		{"no files", &IncludeSource{Dir: src}, t.TempDir()},
		{"destination is main worktree", &IncludeSource{Dir: src, Files: []string{".env"}}, src},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := ApplyInclude(tt.include, tt.dest, false)
			if err != nil || n != 0 {
				t.Errorf("ApplyInclude = (%d, %v), want (0, nil)", n, err)
			}
		})
	}
}

func TestApplyIncludeThenSharedCopyWins(t *testing.T) {
	root := t.TempDir()
	src := t.TempDir()
	wt := t.TempDir()
	writeTestFile(t, filepath.Join(src, ".env"), "from-main")
	writeTestFile(t, filepath.Join(src, "only-main"), "main")
	writeTestFile(t, filepath.Join(root, "shared", "copy", ".env"), "from-shared")

	cfg := &config.Config{SharedDir: config.DefaultSharedDir}
	include := &IncludeSource{Dir: src, Files: []string{".env", "only-main"}}
	result, err := Apply(root, wt, cfg, false, nil, include)
	if err != nil {
		t.Fatal(err)
	}
	if result.Included != 2 || result.Copied != 1 {
		t.Errorf("result = %+v, want Included=2 Copied=1", result)
	}
	if got := readTestFile(t, filepath.Join(wt, ".env")); got != "from-shared" {
		t.Errorf(".env = %q, want shared/copy to win", got)
	}
	if got := readTestFile(t, filepath.Join(wt, "only-main")); got != "main" {
		t.Errorf("only-main = %q", got)
	}
}
