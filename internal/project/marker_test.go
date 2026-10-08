package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateCloneName(t *testing.T) {
	for _, name := range []string{"myrepo", "my-repo.v2", ".hidden"} {
		if err := ValidateCloneName(name); err != nil {
			t.Errorf("ValidateCloneName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "../x"} {
		if err := ValidateCloneName(name); err == nil {
			t.Errorf("ValidateCloneName(%q) = nil, want error", name)
		}
	}
}

func TestCloneDirFor(t *testing.T) {
	home := t.TempDir()
	t.Setenv(WtxHomeEnv, home)
	got, err := CloneDirFor("myrepo")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "myrepo"); got != want {
		t.Errorf("CloneDirFor = %q, want %q", got, want)
	}
	if _, err := CloneDirFor(".."); err == nil {
		t.Error("expected error for ..")
	}
}

func TestMarkerRoundTripAndCheck(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myrepo")
	root := t.TempDir()
	other := t.TempDir()

	// Missing dir is free.
	if err := CheckCloneDir(dir, root); err != nil {
		t.Fatalf("CheckCloneDir(missing) = %v", err)
	}

	// Dry run writes nothing.
	if err := WriteMarker(dir, root, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run created %s", dir)
	}

	if err := WriteMarker(dir, root, false); err != nil {
		t.Fatal(err)
	}
	m, err := ReadMarker(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Root != root {
		t.Errorf("marker root = %q, want %q", m.Root, root)
	}

	if err := CheckCloneDir(dir, root); err != nil {
		t.Errorf("CheckCloneDir(same root) = %v", err)
	}
	if err := CheckCloneDir(dir, other); !errors.Is(err, ErrCloneDirTaken) {
		t.Errorf("CheckCloneDir(other root) = %v, want ErrCloneDirTaken", err)
	}

	// An existing directory without a marker is never adopted.
	bare := t.TempDir()
	if err := CheckCloneDir(bare, root); !errors.Is(err, ErrCloneDirTaken) {
		t.Errorf("CheckCloneDir(no marker) = %v, want ErrCloneDirTaken", err)
	}
}

func TestCheckHomeDirSymlinkedRoot(t *testing.T) {
	realRoot := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "r")
	if err := WriteMarker(dir, link, false); err != nil {
		t.Fatal(err)
	}
	if err := CheckCloneDir(dir, realRoot); err != nil {
		t.Errorf("symlinked spelling of the same root rejected: %v", err)
	}
}

func TestFindRootFromWtxHome(t *testing.T) {
	wtxHome := t.TempDir()
	t.Setenv(WtxHomeEnv, wtxHome)

	root := t.TempDir()
	if err := saveConfig(root); err != nil {
		t.Fatal(err)
	}
	projDir := filepath.Join(wtxHome, "myrepo")
	shared := filepath.Join(projDir, "shared", "copy")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarker(projDir, root, false); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{projDir, shared} {
		got, err := FindRoot(dir)
		if err != nil {
			t.Fatalf("FindRoot(%s) error: %v", dir, err)
		}
		assertSameDir(t, got, root)
	}

	// The wtx home itself, and directories without a marker, do not match.
	if got, ok := RootFromMarker(wtxHome); ok {
		t.Errorf("RootFromMarker(home) = %q, want no match", got)
	}
	orphan := filepath.Join(wtxHome, "orphan", "shared")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := RootFromMarker(orphan); ok {
		t.Errorf("RootFromMarker(orphan) = %q, want no match", got)
	}

	// A marker whose root lost its .worktree.yml does not match.
	stale := filepath.Join(wtxHome, "stale")
	if err := WriteMarker(stale, t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	if got, ok := RootFromMarker(stale); ok {
		t.Errorf("RootFromMarker(stale) = %q, want no match", got)
	}

	// A project.yml outside the wtx home is ignored.
	elsewhere := filepath.Join(t.TempDir(), "x")
	if err := WriteMarker(elsewhere, root, false); err != nil {
		t.Fatal(err)
	}
	if got, ok := RootFromMarker(elsewhere); ok {
		t.Errorf("RootFromMarker(outside home) = %q, want no match", got)
	}
}
