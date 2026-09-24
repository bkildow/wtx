package doctor

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/git"
)

func TestGitVersionThresholds(t *testing.T) {
	bin := resolved(t.TempDir())
	t.Setenv("PATH", bin)
	for _, tt := range []struct {
		version  string
		severity Severity
	}{{"2.19.9", "fail"}, {"2.20.0", "warn"}, {"2.47.9", "warn"}, {"2.48.0", "ok"}, {"3.0.0", "ok"}} {
		write(t, filepath.Join(bin, "git"), "#!/bin/sh\necho 'git version "+tt.version+"'\n", 0o755)
		s := &inspection{runner: git.NewRunner("", false)}
		s.gitVersion(context.Background())
		if len(s.report.Findings) != 1 || s.report.Findings[0].Severity != tt.severity {
			t.Fatalf("Git %s: %+v", tt.version, s.report)
		}
	}
}

func TestPartialRepairWriteFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission failure requires non-root user")
	}
	root, gitDir, _ := fixture(t, false)
	bin := resolved(t.TempDir())
	write(t, filepath.Join(bin, "wtx"), "#!/bin/sh\nexit 99", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(root, ".claude", "settings.local.json")
	input := `{"hooks":{"WorktreeCreate":[{"hooks":[{"type":"command","command":"wt claude hook-worktree-create"}]}]}}`
	write(t, path, input, 0o600)
	write(t, filepath.Join(gitDir, "info", "exclude"), "custom\n", 0o600)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil { //nolint:gosec // Directory traversal is required to test a write-permission failure.
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // Restore owner-only directory access for cleanup.
			t.Error(err)
		}
	})
	r := Run(context.Background(), Options{StartDir: root, Fix: true})
	if !r.Unsuccessful(false) {
		t.Fatal("failed write must be unsuccessful")
	}
	statuses := map[string]RepairStatus{}
	for _, outcome := range r.Repairs {
		statuses[outcome.ID] = outcome.Status
	}
	if statuses["claude.hooks"] != "failed" || statuses["git.exclude"] != "applied" {
		t.Fatalf("partial repair results hidden: %+v", r)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != input {
		t.Fatal("failed repair changed settings")
	}
}

func TestMissingExcludeParentAndNewFileGuard(t *testing.T) {
	root, gitDir, _ := fixture(t, false)
	info := filepath.Join(gitDir, "info")
	if err := os.RemoveAll(info); err != nil {
		t.Fatal(err)
	}
	r := Run(context.Background(), Options{StartDir: root, Fix: true, DryRun: true})
	if _, err := os.Stat(info); !os.IsNotExist(err) {
		t.Fatal("preview created info directory")
	}
	if len(r.Repairs) != 1 {
		t.Fatalf("missing exclusions not planned: %+v", r)
	}
	r = Run(context.Background(), Options{StartDir: root, Fix: true})
	if r.Unsuccessful(false) {
		t.Fatalf("missing-parent repair: %+v", r)
	}
	path := filepath.Join(root, "new-file")
	file, err := takeSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, "concurrent creation", 0o600)
	repair := repair{file: file, data: []byte("replacement")}
	if _, err := repair.apply(context.Background()); err == nil {
		t.Fatal("overwrote concurrently created file")
	}
}

func TestScanSkipsDirectoryLinksAndConfiguredDependencyPaths(t *testing.T) {
	root, _, _ := fixture(t, false)
	external := resolved(t.TempDir())
	write(t, filepath.Join(external, "secret.sh"), "wt status # do not scan", 0o600)
	if err := os.Symlink(external, filepath.Join(root, "bin", "outside")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "bin", "node_modules", "tool"), "wt status", 0o755)
	s := inspect(context.Background(), Options{StartDir: root})
	s.scanFile(filepath.Join(root, "bin", "outside", "secret.sh"), scopeProject)
	for _, f := range s.report.Findings {
		if f.ID == "migration.references" {
			t.Fatalf("followed excluded path: %+v", f)
		}
	}
}

func TestRepairRefusesRetargetedParentAndChecksVerification(t *testing.T) {
	root := resolved(t.TempDir())
	for _, name := range []string{"one", "two"} {
		write(t, filepath.Join(root, name, "data"), "before", 0o600)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "one"), link); err != nil {
		t.Fatal(err)
	}
	file, err := takeSnapshot(filepath.Join(link, "data"))
	if err != nil {
		t.Fatal(err)
	}
	r := repair{file: file, data: []byte("after")}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "two"), link); err != nil {
		t.Fatal(err)
	}
	before := files(t, root)
	if _, err := r.apply(context.Background()); err == nil {
		t.Fatal("accepted retargeted parent")
	}
	if !reflect.DeepEqual(before, files(t, root)) {
		t.Fatal("retargeted repair changed files")
	}
	if err := r.verify(context.Background()); err == nil {
		t.Fatal("verification accepted wrong contents")
	}
}

func TestDiskDisableEnvironmentPrecedence(t *testing.T) {
	root, _, _ := fixture(t, false)
	write(t, filepath.Join(root, ".worktree.yml"), "git_dir: .git\ndisk_warn_percent: 101\n", 0o600)
	t.Setenv("WT_NO_DISK_WARN", "1")
	t.Setenv("WTX_NO_DISK_WARN", "")
	r := Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "disk", ""); f == nil || f.Severity != "warn" {
		t.Fatal("empty new variable must override legacy disable")
	}
	t.Setenv("WTX_NO_DISK_WARN", "1")
	r = Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "disk", ""); f == nil || !strings.Contains(f.Explanation, "disabled") {
		t.Fatal("new disable ignored")
	}
}

func TestScanIgnoresSkippedNamesAboveProject(t *testing.T) {
	src, _, _ := fixture(t, false)
	root := filepath.Join(resolved(t.TempDir()), "build", "proj")
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(src, root); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "bin", "check"), "#!/bin/sh\necho $WT_PROJECT_ROOT\n", 0o750)
	r := Run(context.Background(), Options{StartDir: root})
	if finding(r, "migration.references", filepath.Join(root, "bin", "check")) == nil {
		t.Fatalf("project under a build/ ancestor was not scanned: %+v", r)
	}
}

func TestTrackedScanCandidates(t *testing.T) {
	root, gitDir, _ := fixture(t, false)
	write(t, filepath.Join(root, "main.go"), "for _, wt := range xs { _ = wt }\n", 0o600)
	write(t, filepath.Join(root, "deploy.sh"), "wt add feature\n", 0o600)
	write(t, filepath.Join(root, "compat.sh"), "root=\"${WTX_PROJECT_ROOT:-${WT_PROJECT_ROOT:-}}\"\n", 0o600)
	write(t, filepath.Join(root, "a.bin"), "\x00", 0o600)
	write(t, filepath.Join(root, "b.bin"), "\x00", 0o600)
	gitRun(t, "--git-dir", gitDir, "--work-tree", root, "add", "main.go", "deploy.sh", "compat.sh", "a.bin", "b.bin")
	r := Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "migration.references", filepath.Join(root, "main.go")); f != nil {
		t.Fatalf("Go identifier reported: %+v", f)
	}
	if f := finding(r, "migration.references", filepath.Join(root, "compat.sh")); f != nil {
		t.Fatalf("WTX_/WT_ fallback reported: %+v", f)
	}
	if finding(r, "migration.references", filepath.Join(root, "deploy.sh")) == nil {
		t.Fatal("shell wt invocation missed")
	}
	limits := 0
	for _, f := range r.Findings {
		if strings.HasPrefix(f.Explanation, "Scan limitation") {
			limits++
		}
	}
	if limits != 1 {
		t.Fatalf("scan limitations not aggregated: %d", limits)
	}
}

func TestTrackedScanKeepsWhitespacePaths(t *testing.T) {
	root, gitDir, _ := fixture(t, false)
	write(t, filepath.Join(root, " first.sh"), "echo $WT_PROJECT_ROOT\n", 0o600)
	gitRun(t, "--git-dir", gitDir, "--work-tree", root, "add", " first.sh")
	r := Run(context.Background(), Options{StartDir: root})
	if finding(r, "migration.references", filepath.Join(root, " first.sh")) == nil {
		t.Fatalf("leading-space tracked path skipped: %+v", r)
	}
}
