package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
)

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@t")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func fixture(t *testing.T, bare bool) (root, gitDir, worktree string) {
	t.Helper()
	root = resolved(t.TempDir())
	gitDir = filepath.Join(root, ".git")
	worktree = root
	if bare {
		src := t.TempDir()
		gitRun(t, "init", "-b", "main", src)
		gitRun(t, "-C", src, "commit", "--allow-empty", "-m", "initial")
		gitDir = filepath.Join(root, ".bare")
		gitRun(t, "clone", "--bare", src, gitDir)
		worktree = filepath.Join(root, "worktrees", "main")
		gitRun(t, "--git-dir", gitDir, "worktree", "add", worktree, "main")
	} else {
		gitRun(t, "init", "-b", "main", root)
		gitRun(t, "-C", root, "commit", "--allow-empty", "-m", "initial")
	}
	cfg := config.DefaultConfig()
	cfg.GitDir = filepath.Base(gitDir)
	cfg.Scripts = map[string]string{"check": "bin/check"}
	no := false
	cfg.DiskWarn = &no
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "bin", "check"), "#!/bin/sh\nexit 99\n", 0o750)
	if err := project.EnsureGitExclude(gitDir, false); err != nil {
		t.Fatal(err)
	}
	return root, gitDir, worktree
}

func files(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		var data []byte
		if d.Type()&os.ModeSymlink != 0 {
			target, e := os.Readlink(path)
			err = e
			data = []byte(target)
		} else {
			data, err = os.ReadFile(path)
		}
		if err != nil {
			return err
		}
		result[path] = fmt.Sprintf("%s:%s", info.Mode(), data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func finding(report Report, id, path string) *Finding {
	for _, f := range report.Findings {
		if f.ID == id && (path == "" || f.Path == path) {
			return &f
		}
	}
	return nil
}

func TestReadOnlyPreviewAndGitRepairs(t *testing.T) {
	for _, bare := range []bool{false, true} {
		t.Run(fmt.Sprint(bare), func(t *testing.T) {
			root, gitDir, wt := fixture(t, bare)
			exclude := filepath.Join(gitDir, "info", "exclude")
			write(t, exclude, "# custom\nprivate/\n# wt-cli managed files\n.wt-setup.json\n", 0o640)
			if err := os.Chmod(exclude, 0o640); err != nil { //nolint:gosec // Verify preservation of an existing group-readable file.
				t.Fatal(err)
			}
			old := files(t, root)
			for _, opts := range []Options{{StartDir: wt}, {StartDir: wt, Fix: true, DryRun: true}} {
				r := Run(context.Background(), opts)
				if f := finding(r, "git.exclude", exclude); f == nil || !f.Repairable {
					t.Fatalf("missing exclusion repair: %+v", r)
				}
				if !reflect.DeepEqual(old, files(t, root)) {
					t.Fatal("inspection or preview changed project")
				}
				for _, repair := range r.Repairs {
					if repair.Status != "planned" || repair.Backup != "" {
						t.Fatalf("bad preview: %+v", repair)
					}
				}
			}
			r := Run(context.Background(), Options{StartDir: root, Fix: true})
			if r.Unsuccessful(false) {
				t.Fatalf("repair failed: %+v", r)
			}
			for _, repair := range r.Repairs {
				if repair.Status != "applied" {
					t.Fatalf("bad repair: %+v", repair)
				}
			}
			data, err := os.ReadFile(exclude)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte("# custom\nprivate/\n")) || bytes.Contains(data, []byte("# wt-cli")) {
				t.Fatalf("custom exclusions changed: %s", data)
			}
			info, _ := os.Stat(exclude)
			if info.Mode().Perm() != 0o640 {
				t.Fatal("permissions changed")
			}
			backups, err := filepath.Glob(exclude + ".wtx-backup-*")
			if err != nil || len(backups) != 1 {
				t.Fatalf("backups: %v %v", backups, err)
			}
			if bare {
				gitRun(t, "-C", wt, "status", "--porcelain")
				path, err := git.WorktreeConfigPath(wt)
				if err != nil {
					t.Fatal(err)
				}
				if value, err := git.ConfigBool(context.Background(), path, "core.bare"); err != nil || value != "false" {
					t.Fatalf("bare override: %q %v", value, err)
				}
			}
			after := files(t, root)
			r = Run(context.Background(), Options{StartDir: root, Fix: true})
			if len(r.Repairs) != 0 || !reflect.DeepEqual(after, files(t, root)) {
				t.Fatalf("not idempotent: %+v", r.Repairs)
			}
		})
	}
}

func TestSetupRecordsAndPartialRepairs(t *testing.T) {
	root, _, wt := fixture(t, true)
	legacy := filepath.Join(wt, ".wt-setup.json")
	write(t, legacy, `{"status":"running","pid":2147483647,"hooks_total":2,"hooks_completed":1}`, 0o640)
	log := filepath.Join(wt, ".wt-setup.log")
	write(t, log, "keep this log", 0o600)
	r := Run(context.Background(), Options{StartDir: root, Fix: true})
	if !r.Unsuccessful(false) {
		t.Fatal("failed setup must remain unsuccessful")
	}
	f := finding(r, "setup.state", legacy)
	if f == nil || f.Repairable || f.Severity != "fail" {
		t.Fatalf("bad reconciled state: %+v", f)
	}
	if data, _ := os.ReadFile(log); string(data) != "keep this log" {
		t.Fatal("log changed")
	}
	if _, err := os.Stat(filepath.Join(wt, project.SetupStateFile)); !os.IsNotExist(err) {
		t.Fatal("created new state over legacy state")
	}
	write(t, filepath.Join(wt, project.SetupStateFile), fmt.Sprintf(`{"status":"running","pid":%d}`, os.Getpid()), 0o600)
	before := files(t, root)
	Run(context.Background(), Options{StartDir: root, Fix: true})
	if !reflect.DeepEqual(before, files(t, root)) {
		t.Fatal("live process or already-reconciled state changed")
	}
	for _, data := range []string{`{`, `null`, `{"status":"bogus"}`, `{"status":"running","pid":0}`, `{"status":"complete","hooks_total":0,"hooks_completed":1}`} {
		write(t, filepath.Join(wt, project.SetupStateFile), data, 0o600)
		r = Run(context.Background(), Options{StartDir: root})
		if f := finding(r, "setup.state", filepath.Join(wt, project.SetupStateFile)); f == nil || f.Severity != "fail" || f.Repairable {
			t.Fatalf("bad malformed state finding: %+v", f)
		}
	}
}

func TestRepairGuardsBackupsAndFailure(t *testing.T) {
	root, gitDir, _ := fixture(t, false)
	path := filepath.Join(gitDir, "info", "exclude")
	write(t, path, "first\n", 0o600)
	s := inspect(context.Background(), Options{StartDir: root})
	write(t, path, "concurrent edit\n", 0o600)
	if len(s.repairs) != 1 {
		t.Fatalf("unexpected repairs %v", s.repairs)
	}
	if _, err := s.repairs[0].apply(context.Background()); err == nil {
		t.Fatal("accepted concurrent edit")
	}
	if data, _ := os.ReadFile(path); string(data) != "concurrent edit\n" {
		t.Fatal("overwrote edit")
	}
	Run(context.Background(), Options{StartDir: root, Fix: true})
	backups, _ := filepath.Glob(path + ".wtx-backup-*")
	if len(backups) != 1 {
		t.Fatalf("backups %v", backups)
	}
	first, _ := os.ReadFile(backups[0])
	write(t, path, "third\n", 0o600)
	Run(context.Background(), Options{StartDir: root, Fix: true})
	all, _ := filepath.Glob(path + ".wtx-backup-*")
	if len(all) != 2 {
		t.Fatalf("backups overwritten: %v", all)
	}
	if data, _ := os.ReadFile(backups[0]); !bytes.Equal(first, data) {
		t.Fatal("previous backup overwritten")
	}
	write(t, path, "fourth\n", 0o600)
	s = inspect(context.Background(), Options{StartDir: root})
	write(t, filepath.Join(root, config.ConfigFileName), "version: 2\n", 0o600)
	if _, err := s.repairs[0].apply(context.Background()); err == nil {
		t.Fatal("accepted changed configuration")
	}
	file, err := takeSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	r := repair{file: file, key: "not-a-valid-key", value: "true"}
	if _, err := r.apply(context.Background()); err == nil {
		t.Fatal("expected staged Git write failure")
	}
	if data, _ := os.ReadFile(path); string(data) != "fourth\n" {
		t.Fatal("failed write changed original")
	}
}

func TestScriptsTeardownDiskAndShared(t *testing.T) {
	root, gitDir, _ := fixture(t, false)
	wt := filepath.Join(root, "worktrees", "feature")
	gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", "feature", wt)
	write(t, filepath.Join(root, "compose.yaml"), "services: {}\n", 0o600)
	write(t, filepath.Join(wt, "docker-compose.yml"), "services: {}\n", 0o600)
	write(t, filepath.Join(root, "shared", "copy", ".env.template"), "from template", 0o600)
	write(t, filepath.Join(wt, ".env"), "local content differs", 0o600)
	write(t, filepath.Join(root, "shared", "copy", "missing"), "shared", 0o600)
	write(t, filepath.Join(root, "shared", "symlink", ".claude", "rules", "one.md"), "rule", 0o600)
	if err := os.MkdirAll(filepath.Join(wt, ".claude", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	deletedLink := filepath.Join(wt, ".claude", "deleted")
	if err := os.Symlink(filepath.Join(root, "shared", "symlink", ".claude", "deleted"), deletedLink); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Scripts = map[string]string{"missing": "missing", "directory": "bin", "nonexec": "compose.yaml"}
	yes := true
	cfg.DiskWarn = &yes
	cfg.DiskWarnPercent = 101
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WTX_NO_DISK_WARN", "")
	before := files(t, root)
	r := Run(context.Background(), Options{StartDir: root, Fix: true})
	if !reflect.DeepEqual(before, files(t, root)) {
		t.Fatal("report-only findings changed project")
	}
	for _, id := range []string{"scripts", "teardown", "disk", "shared.copy", "shared.symlink"} {
		if f := finding(r, id, ""); f == nil || f.Severity == "ok" || f.Repairable {
			t.Fatalf("missing %s: %+v", id, r)
		}
	}
	if f := finding(r, "shared.copy", filepath.Join(wt, ".env")); f != nil {
		t.Fatalf("existing template output should be accepted: %+v", f)
	}
	if finding(r, "shared.symlink", deletedLink) == nil {
		t.Fatal("link to deleted shared source was missed")
	}
	if f := finding(r, "teardown", filepath.Join(wt, "docker-compose.yml")); f == nil {
		t.Fatal("worktree Compose omitted")
	}
	cfg.Scripts = nil
	cfg.Teardown = []string{"review-cleanup"}
	cfg.DiskWarnPercent = -1
	cfg.DiskWarnGB = -1
	if err := cfg.Save(root); err != nil {
		t.Fatal(err)
	}
	r = Run(context.Background(), Options{StartDir: root})
	if f := finding(r, "scripts", ""); f == nil || f.Severity != "warn" {
		t.Fatal("empty scripts should warn")
	}
	if finding(r, "teardown", "") != nil {
		t.Fatal("warned despite teardown")
	}
	if f := finding(r, "disk", ""); f == nil || f.Severity != "ok" {
		t.Fatal("negative disk thresholds should disable bounds")
	}
}

func TestRegistrationsAndBranches(t *testing.T) {
	root, gitDir, _ := fixture(t, false)
	for _, name := range []string{"missing", "locked"} {
		path := filepath.Join(root, "worktrees", name)
		gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", name, path)
		if name == "locked" {
			gitRun(t, "--git-dir", gitDir, "worktree", "lock", path)
		}
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, "--git-dir", gitDir, "branch", "orphan")
	gitRun(t, "--git-dir", gitDir, "branch", "remote-present")
	gitRun(t, "--git-dir", gitDir, "update-ref", "refs/remotes/upstream/remote-present", "HEAD")
	before := files(t, root)
	r := Run(context.Background(), Options{StartDir: root, Fix: true})
	if !reflect.DeepEqual(before, files(t, root)) {
		t.Fatal("changed branches or registrations")
	}
	for _, name := range []string{"missing", "locked"} {
		f := finding(r, "git.worktrees", filepath.Join(root, "worktrees", name))
		if f == nil || f.Severity != "warn" {
			t.Fatalf("missing registration: %+v", f)
		}
		if name == "locked" && !strings.Contains(f.Explanation, "locked") {
			t.Fatalf("lock omitted: %+v", f)
		}
		if name == "missing" && !strings.Contains(f.Remedy, "prune --dry-run") {
			t.Fatalf("prune omitted: %+v", f)
		}
	}
	count := 0
	for _, f := range r.Findings {
		if f.ID == "git.branches" {
			count++
			if !strings.Contains(f.Explanation, "orphan") {
				t.Fatalf("wrong orphan: %+v", f)
			}
		}
	}
	if count != 1 {
		t.Fatalf("orphan count %d", count)
	}
}

func TestScanBoundsAndUserDiscovery(t *testing.T) {
	root, gitDir, _ := fixture(t, false)
	write(t, filepath.Join(root, "tracked.sh"), "wt status # SECRET_TOKEN\necho $WT_PROJECT_ROOT $WT_THEME\nwt-cli .wt-setup.json wtx\n", 0o600)
	write(t, filepath.Join(root, "untracked.sh"), "wt status", 0o600)
	write(t, filepath.Join(root, "node_modules", "dep.sh"), "wt status", 0o600)
	write(t, filepath.Join(root, "binary"), "wt\x00", 0o600)
	write(t, filepath.Join(root, "large"), strings.Repeat("x", scanLimit+1)+"wt", 0o600)
	gitRun(t, "--git-dir", gitDir, "--work-tree", root, "add", "tracked.sh", "node_modules", "binary", "large")
	r := Run(context.Background(), Options{StartDir: root})
	count := 0
	for _, f := range r.Findings {
		if f.ID == "migration.references" {
			count++
			if filepath.Base(f.Path) != "tracked.sh" || f.Line > 2 {
				t.Fatalf("unexpected scan candidate: %+v", f)
			}
		}
	}
	if count != 3 {
		t.Fatalf("scan matches %d: %+v", count, r)
	}
	data, _ := json.Marshal(r)
	if bytes.Contains(data, []byte("SECRET_TOKEN")) {
		t.Fatal("scan leaked source line")
	}
	home := resolved(t.TempDir())
	zdir := filepath.Join(home, "zsh")
	xdg := filepath.Join(home, "config")
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", zdir)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("WT_THEME", "secret-value")
	write(t, filepath.Join(home, ".bashrc"), "eval \"$(wt shell-init bash)\"", 0o600)
	write(t, filepath.Join(zdir, ".zshrc"), "function wt() { command wt \"$@\"; }", 0o600)
	write(t, filepath.Join(xdg, "fish", "conf.d", "wtx.fish"), "wt shell-init fish | source", 0o600)
	before := files(t, home)
	r = Run(context.Background(), Options{StartDir: home, User: true, Fix: true})
	if !r.Unsuccessful(false) || !reflect.DeepEqual(before, files(t, home)) {
		t.Fatal("user fix must be rejected without edits")
	}
	for _, path := range []string{filepath.Join(home, ".bashrc"), filepath.Join(zdir, ".zshrc"), filepath.Join(xdg, "fish", "conf.d", "wtx.fish")} {
		if finding(r, "migration.references", path) == nil {
			t.Fatalf("startup file missed: %s", path)
		}
	}
	data, _ = json.Marshal(r)
	if bytes.Contains(data, []byte("secret-value")) {
		t.Fatal("user environment value leaked")
	}
}

func TestOperationalReportsAndStrict(t *testing.T) {
	r := Run(context.Background(), Options{StartDir: t.TempDir()})
	if r.SchemaVersion != 1 || !r.Unsuccessful(false) {
		t.Fatalf("invalid discovery failure report: %+v", r)
	}
	root, _, _ := fixture(t, false)
	write(t, filepath.Join(root, ".worktree.yml"), "scripts: {}\ndisk_warn: false\ngit_dir: .git\n", 0o600)
	r = Run(context.Background(), Options{StartDir: root})
	if r.Unsuccessful(false) || !r.Unsuccessful(true) {
		t.Fatalf("wrong warning exits: %+v", r)
	}
	write(t, filepath.Join(root, ".worktree.yml"), "[malformed", 0o600)
	r = Run(context.Background(), Options{StartDir: root})
	if !r.Unsuccessful(false) || finding(r, "scripts", "") == nil {
		t.Fatal("missing blocked checks")
	}
}
