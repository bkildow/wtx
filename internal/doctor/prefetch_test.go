package doctor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func findings(report Report, id string) []Finding {
	var out []Finding
	for _, f := range report.Findings {
		if f.ID == id {
			out = append(out, f)
		}
	}
	return out
}

func TestSharedCopyMissingDirectoryReportedOnce(t *testing.T) {
	root, gitDir, _ := fixture(t, true)
	wt := filepath.Join(root, "worktrees", "feature")
	gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", "feature", wt)
	for _, name := range []string{"a", "b", "sub/c"} {
		write(t, filepath.Join(root, "shared", "copy", "config", name), "x", 0o600)
	}
	write(t, filepath.Join(root, "shared", "copy", "top"), "x", 0o600)

	r := Run(context.Background(), Options{StartDir: root})
	got := map[string]string{}
	for _, f := range findings(r, "shared.copy") {
		got[f.Path] = f.Explanation
	}
	for _, base := range []string{filepath.Join(root, "worktrees", "main"), wt} {
		if got[filepath.Join(base, "config")] != "Expected shared copy directory is missing." {
			t.Errorf("missing directory not collapsed for %s: %v", base, got)
		}
		if got[filepath.Join(base, "top")] != "Expected shared copy is missing." {
			t.Errorf("missing file not reported for %s: %v", base, got)
		}
	}
	if len(got) != 4 {
		t.Fatalf("want 4 shared.copy findings, got %v", got)
	}
}

func TestSharedCopyDependencyDirectoryCheckedForPresenceOnly(t *testing.T) {
	root, gitDir, _ := fixture(t, true)
	wt := filepath.Join(root, "worktrees", "feature")
	gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", "feature", wt)
	write(t, filepath.Join(root, "shared", "copy", "vendor", "pkg", "removed.php"), "x", 0o600)
	// Setup tools rewrite dependency trees, so differing contents are fine.
	write(t, filepath.Join(wt, "vendor", "pkg", "other.php"), "y", 0o600)

	r := Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "shared.copy", filepath.Join(wt, "vendor", "pkg", "removed.php")); f != nil {
		t.Fatalf("dependency directory contents should not be compared: %+v", f)
	}
	main := filepath.Join(root, "worktrees", "main", "vendor")
	if f := finding(r, "shared.copy", main); f == nil || f.Explanation != "Expected shared copy directory is missing." {
		t.Fatalf("missing dependency directory not reported: %+v", r.Findings)
	}
}

func TestTrackedReferencesNotRepeatedAcrossWorktrees(t *testing.T) {
	root, gitDir, main := fixture(t, true)
	write(t, filepath.Join(main, "run.sh"), "#!/bin/sh\nwt list\n", 0o755)
	gitRun(t, "-C", main, "add", "run.sh")
	gitRun(t, "-C", main, "commit", "-m", "add script")
	wt := filepath.Join(root, "worktrees", "feature")
	gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", "feature", wt)
	edited := filepath.Join(root, "worktrees", "edited")
	gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", "edited", edited)
	write(t, filepath.Join(edited, "run.sh"), "#!/bin/sh\nwt add x\n", 0o755)

	r := Run(context.Background(), Options{StartDir: root})
	var paths []string
	for _, f := range findings(r, "migration.references") {
		paths = append(paths, f.Path)
	}
	// The identical blob is reported once, in whichever worktree Git lists first.
	a := finding(r, "migration.references", filepath.Join(main, "run.sh")) != nil
	b := finding(r, "migration.references", filepath.Join(wt, "run.sh")) != nil
	if a == b {
		t.Fatalf("unmodified copies should be reported exactly once: %v", paths)
	}
	if finding(r, "migration.references", filepath.Join(edited, "run.sh")) == nil {
		t.Fatalf("locally modified copy must be scanned: %v", paths)
	}
}

func TestDanglingLinkOutsideSharedMirrorIgnored(t *testing.T) {
	root, gitDir, _ := fixture(t, true)
	wt := filepath.Join(root, "worktrees", "feature")
	gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", "feature", wt)
	write(t, filepath.Join(root, "shared", "symlink", ".claude", "kept.md"), "x", 0o600)
	if err := os.MkdirAll(filepath.Join(wt, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(wt, ".claude", "gone.md")
	if err := os.Symlink(filepath.Join(root, "shared", "symlink", ".claude", "gone.md"), gone); err != nil {
		t.Fatal(err)
	}
	// Managed links only live in mirrors of shared/symlink; other trees are
	// not walked (they can be enormous, e.g. a Drupal docroot).
	if err := os.MkdirAll(filepath.Join(wt, "docroot"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(wt, "docroot", "gone.md")
	if err := os.Symlink(filepath.Join(root, "shared", "symlink", "docroot", "gone.md"), outside); err != nil {
		t.Fatal(err)
	}
	r := Run(context.Background(), Options{StartDir: root})
	if finding(r, "shared.symlink", gone) == nil {
		t.Fatalf("dangling link in mirrored directory missed: %+v", r.Findings)
	}
	if f := finding(r, "shared.symlink", outside); f != nil {
		t.Fatalf("directories outside the shared mirror should not be walked: %+v", f)
	}
}
