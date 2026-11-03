package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bkildow/wtx/internal/git"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func readlink(t *testing.T, path string) string {
	t.Helper()
	dest, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("%s is not a symlink: %v", path, err)
	}
	return dest
}

func TestCheckShareSource(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, ".env"), "x")
	mustSymlink(t, filepath.Join(src, ".env"), filepath.Join(src, "link"))
	mustSymlink(t, t.TempDir(), filepath.Join(src, "linkdir"))

	for _, tt := range []struct {
		rel     string
		wantErr bool
	}{
		{".env", false},
		{"missing", true},
		{"link", true},
		{"linkdir/file", true},
	} {
		if err := CheckShareSource(src, tt.rel); (err != nil) != tt.wantErr {
			t.Errorf("CheckShareSource(%s) = %v, wantErr %v", tt.rel, err, tt.wantErr)
		}
	}
}

func TestLinkBack(t *testing.T) {
	root, symlinkDir := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(root, "config", "local.yml"), "x")

	if already, err := LinkBack(symlinkDir, root, "config/local.yml", true); err != nil || already {
		t.Fatalf("dry run: %v %v", already, err)
	}
	if _, err := os.Lstat(filepath.Join(symlinkDir, "config")); !os.IsNotExist(err) {
		t.Fatal("dry run created the shared entry")
	}

	if already, err := LinkBack(symlinkDir, root, "config/local.yml", false); err != nil || already {
		t.Fatalf("LinkBack: %v %v", already, err)
	}
	entry := filepath.Join(symlinkDir, "config", "local.yml")
	if got := readlink(t, entry); got != filepath.Join(root, "config", "local.yml") {
		t.Errorf("entry -> %s", got)
	}
	if already, err := LinkBack(symlinkDir, root, "config/local.yml", false); err != nil || !already {
		t.Errorf("second LinkBack: %v %v, want already", already, err)
	}

	mustWrite(t, filepath.Join(root, ".env"), "root")
	mustWrite(t, filepath.Join(symlinkDir, ".env"), "shared")
	if _, err := LinkBack(symlinkDir, root, ".env", false); err == nil {
		t.Error("LinkBack replaced an existing shared entry")
	}
}

func TestMoveToShared(t *testing.T) {
	wt, symlinkDir := t.TempDir(), t.TempDir()
	src, dst := filepath.Join(wt, ".env"), filepath.Join(symlinkDir, ".env")
	mustWrite(t, src, "x")

	if err := MoveToShared(symlinkDir, wt, ".env", true); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(src); err != nil || !info.Mode().IsRegular() {
		t.Fatal("dry run moved the file")
	}

	if err := MoveToShared(symlinkDir, wt, ".env", false); err != nil {
		t.Fatal(err)
	}
	if got := readlink(t, src); got != dst {
		t.Errorf("source -> %s, want %s", got, dst)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "x" {
		t.Errorf("moved file: %q %v", data, err)
	}

	mustWrite(t, filepath.Join(wt, "other"), "y")
	mustWrite(t, filepath.Join(symlinkDir, "other"), "z")
	if err := MoveToShared(symlinkDir, wt, "other", false); err == nil {
		t.Error("MoveToShared replaced an existing shared entry")
	}
}

func TestCopyToShared(t *testing.T) {
	wt, copyDir := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(wt, "certs", "dev.pem"), "pem")

	if err := CopyToShared(copyDir, wt, "certs", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(copyDir, "certs")); !os.IsNotExist(err) {
		t.Fatal("dry run copied")
	}
	if err := CopyToShared(copyDir, wt, "certs", false); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(copyDir, "certs", "dev.pem")); err != nil || string(data) != "pem" {
		t.Errorf("copy: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(wt, "certs", "dev.pem")); err != nil {
		t.Error("source lost its file")
	}
	if err := CopyToShared(copyDir, wt, "certs", false); err == nil {
		t.Error("CopyToShared replaced an existing shared entry")
	}
}

func TestLinkInto(t *testing.T) {
	symlinkDir := t.TempDir()
	mustWrite(t, filepath.Join(symlinkDir, ".env"), "x")
	mustWrite(t, filepath.Join(symlinkDir, "config", "local.yml"), "y")
	target := filepath.Join(symlinkDir, ".env")

	fresh := git.WorktreeInfo{Path: t.TempDir(), Branch: "fresh"}
	linked := git.WorktreeInfo{Path: t.TempDir(), Branch: "linked"}
	mustSymlink(t, target, filepath.Join(linked.Path, ".env"))
	own := git.WorktreeInfo{Path: t.TempDir(), Branch: "own"}
	mustWrite(t, filepath.Join(own.Path, ".env"), "own")
	foreign := git.WorktreeInfo{Path: t.TempDir(), Branch: "foreign"}
	mustSymlink(t, filepath.Join(t.TempDir(), "elsewhere"), filepath.Join(foreign.Path, ".env"))
	wts := []git.WorktreeInfo{fresh, linked, own, foreign}

	out, err := LinkInto(symlinkDir, ".env", wts, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(fresh.Path, ".env")); !os.IsNotExist(err) {
		t.Fatal("dry run made a link")
	}
	if out[0].State != ShareDone {
		t.Errorf("dry run: fresh state = %v", out[0].State)
	}

	out, err = LinkInto(symlinkDir, ".env", wts, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []ShareState{ShareDone, ShareAlready, ShareSkipped, ShareSkipped}
	for i, o := range out {
		if o.State != want[i] {
			t.Errorf("%s: state %v, want %v", o.Worktree.Branch, o.State, want[i])
		}
	}
	if got := readlink(t, filepath.Join(fresh.Path, ".env")); got != target {
		t.Errorf("fresh -> %s", got)
	}
	if data, _ := os.ReadFile(filepath.Join(own.Path, ".env")); string(data) != "own" {
		t.Error("own file was replaced")
	}

	// A nested path links the file inside a real directory, else the
	// top-level directory, as ApplySymlinks does.
	withDir := git.WorktreeInfo{Path: t.TempDir(), Branch: "withdir"}
	if err := os.Mkdir(filepath.Join(withDir.Path, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	noDir := git.WorktreeInfo{Path: t.TempDir(), Branch: "nodir"}
	if _, err := LinkInto(symlinkDir, filepath.Join("config", "local.yml"), []git.WorktreeInfo{withDir, noDir}, false); err != nil {
		t.Fatal(err)
	}
	if got := readlink(t, filepath.Join(withDir.Path, "config", "local.yml")); got != filepath.Join(symlinkDir, "config", "local.yml") {
		t.Errorf("withdir -> %s", got)
	}
	if got := readlink(t, filepath.Join(noDir.Path, "config")); got != filepath.Join(symlinkDir, "config") {
		t.Errorf("nodir -> %s", got)
	}
}

func TestSeedInto(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "certs", "a.pem"), "a")
	mustWrite(t, filepath.Join(src, "certs", "b.pem"), "b")

	fresh := git.WorktreeInfo{Path: t.TempDir(), Branch: "fresh"}
	partial := git.WorktreeInfo{Path: t.TempDir(), Branch: "partial"}
	mustWrite(t, filepath.Join(partial.Path, "certs", "a.pem"), "own")
	full := git.WorktreeInfo{Path: t.TempDir(), Branch: "full"}
	mustWrite(t, filepath.Join(full.Path, "certs", "a.pem"), "own")
	mustWrite(t, filepath.Join(full.Path, "certs", "b.pem"), "own")
	wts := []git.WorktreeInfo{fresh, partial, full}

	if _, err := SeedInto(src, "certs", wts, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(fresh.Path, "certs")); !os.IsNotExist(err) {
		t.Fatal("dry run copied")
	}

	out, err := SeedInto(src, "certs", wts, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []ShareState{ShareDone, ShareDone, ShareSkipped}
	for i, o := range out {
		if o.State != want[i] {
			t.Errorf("%s: state %v, want %v", o.Worktree.Branch, o.State, want[i])
		}
	}
	for path, content := range map[string]string{
		filepath.Join(fresh.Path, "certs", "b.pem"):   "b",
		filepath.Join(partial.Path, "certs", "a.pem"): "own",
		filepath.Join(partial.Path, "certs", "b.pem"): "b",
		filepath.Join(full.Path, "certs", "b.pem"):    "own",
	} {
		if data, _ := os.ReadFile(path); string(data) != content {
			t.Errorf("%s = %q, want %q", path, data, content)
		}
	}
}

func TestAppendWorktreeInclude(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, git.WorktreeIncludeFile)

	if err := AppendWorktreeInclude(root, "/.env", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("dry run wrote the file")
	}
	if err := AppendWorktreeInclude(root, "/.env", false); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, path, "/.env\n# no trailing newline")
	if err := AppendWorktreeInclude(root, "/certs/", false); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "/.env\n# no trailing newline\n/certs/\n" {
		t.Errorf("content = %q", data)
	}
}
