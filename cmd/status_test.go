package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
)

func TestSetupFailureHint(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, project.SetupLogFile)
	if err := os.WriteFile(logPath, []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		state *project.SetupState
		want  string
	}{
		{
			name:  "background failure points at the log",
			state: &project.SetupState{Status: project.SetupFailed, Error: "hook 'npm ci' failed: exit status 1", LogFile: logPath},
			want:  "Setup failed for feat: hook 'npm ci' failed: exit status 1 — run 'wtx logs feat'",
		},
		{
			name:  "multi-line error keeps the first line",
			state: &project.SetupState{Status: project.SetupFailed, Error: "first\nsecond", LogFile: logPath},
			want:  "Setup failed for feat: first — run 'wtx logs feat'",
		},
		{
			name:  "empty error",
			state: &project.SetupState{Status: project.SetupFailed, LogFile: logPath},
			want:  "Setup failed for feat — run 'wtx logs feat'",
		},
		{
			name:  "foreground failure suggests a re-run",
			state: &project.SetupState{Status: project.SetupFailed, Error: "boom"},
			want:  "Setup failed for feat: boom — re-run with 'wtx setup feat --foreground'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := setupFailureHint("feat", dir, tt.state); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestStatusSetupFailureHint(t *testing.T) {
	// runStatusWith writes the given setup state and a log, runs status, and
	// returns its output.
	runStatusWith := func(t *testing.T, state *project.SetupState) string {
		t.Helper()
		worktree := newSetupProject(t)
		t.Setenv("WTX_NO_DISK_WARN", "1")
		state.LogFile = project.SetupLogPath(worktree)
		writeSetupFixture(t, worktree, state, "output\n")
		out := captureUI(t)

		command := newStatusCmd()
		command.SetContext(context.Background())
		if err := runStatus(command, nil); err != nil {
			t.Fatal(err)
		}
		return out.String()
	}

	t.Run("failed", func(t *testing.T) {
		out := runStatusWith(t, &project.SetupState{Status: project.SetupFailed, Error: "hook failed"})
		if !strings.Contains(out, "wtx logs feature") {
			t.Errorf("missing failure hint:\n%s", out)
		}
	})

	t.Run("complete", func(t *testing.T) {
		out := runStatusWith(t, &project.SetupState{Status: project.SetupComplete})
		if strings.Contains(out, "Setup failed") {
			t.Errorf("unexpected failure hint:\n%s", out)
		}
	})
}

func TestGitErrorReason(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "fatal line in stderr wins",
			err:  errors.New("git -C /x status --porcelain: exit status 128\nfatal: gitfile does not point to a valid repository\n"),
			want: "fatal: gitfile does not point to a valid repository",
		},
		{
			name: "no fatal line keeps the first line",
			err:  errors.New("git -C /x log -1: exit status 1\nerror: something\n"),
			want: "git -C /x log -1: exit status 1",
		},
		{
			name: "single line",
			err:  errors.New("boom"),
			want: "boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gitErrorReason(tt.err); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// addBrokenWorktree adds an "alpha" worktree under root whose .git points
// nowhere, so git cannot read its status.
func addBrokenWorktree(t *testing.T, root string) {
	t.Helper()
	broken := filepath.Join(root, "worktrees", "alpha")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-b", "alpha", broken).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: /nonexistent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStatusBrokenWorktree(t *testing.T) {
	worktree := newSetupProject(t)
	addBrokenWorktree(t, filepath.Dir(filepath.Dir(worktree)))
	t.Setenv("WTX_NO_DISK_WARN", "1")
	out := captureUI(t)

	command := newStatusCmd()
	command.SetContext(context.Background())
	if err := runStatus(command, nil); err != nil {
		t.Fatalf("status should not fail on one broken worktree: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Could not read status for alpha: fatal:") {
		t.Errorf("missing broken-worktree hint:\n%s", got)
	}
	rows := map[string]string{"alpha": "error", "feature": "clean"}
	for branch, want := range rows {
		found := false
		for line := range strings.Lines(got) {
			if strings.Contains(line, branch) && !strings.Contains(line, "Could not read") {
				found = true
				if !strings.Contains(line, want) {
					t.Errorf("%s row should show %q: %q", branch, want, line)
				}
			}
		}
		if !found {
			t.Errorf("missing %s row:\n%s", branch, got)
		}
	}
}

func TestStatusJSONSetup(t *testing.T) {
	tests := []struct {
		name  string
		state *project.SetupState
	}{
		{name: "no state"},
		{name: "complete", state: &project.SetupState{Status: project.SetupComplete, HooksTotal: 2, HooksCompleted: 2}},
		{name: "failed", state: &project.SetupState{Status: project.SetupFailed, HooksTotal: 2, HooksCompleted: 1, Error: "hook failed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			worktree := newSetupProject(t)
			if tt.state != nil {
				tt.state.LogFile = project.SetupLogPath(worktree)
				writeSetupFixture(t, worktree, tt.state, "output\n")
			}

			out := runJSON(t, newStatusCmd)
			var report statusReport
			decodeJSON(t, out, &report)
			if report.SchemaVersion != 1 {
				t.Errorf("schema_version = %d, want 1", report.SchemaVersion)
			}
			if len(report.Worktrees) != 1 {
				t.Fatalf("got %d worktrees, want 1:\n%s", len(report.Worktrees), out)
			}
			entry := report.Worktrees[0]
			if entry.Branch != "feature" || !samePath(t, entry.Path, worktree) || len(entry.Head) != 40 {
				t.Errorf("entry = %+v", entry)
			}
			if entry.Dirty == nil || entry.LastCommitAge == nil || entry.Error != "" {
				t.Errorf("readable worktree should have dirty and last_commit_age and no error: %+v", entry)
			}

			if tt.state == nil {
				if entry.Setup != nil || !strings.Contains(out, `"setup": null`) {
					t.Errorf("missing state should be null:\n%s", out)
				}
				return
			}
			if entry.Setup == nil {
				t.Fatalf("missing setup:\n%s", out)
			}
			got := *entry.Setup
			if got.Status != tt.state.Status || got.Error != tt.state.Error || !samePath(t, got.LogFile, tt.state.LogFile) ||
				got.HooksTotal != tt.state.HooksTotal || got.HooksCompleted != tt.state.HooksCompleted {
				t.Errorf("setup = %+v, want %+v", got, *tt.state)
			}
		})
	}
}

// A worktree moved by wtx doctor --migrate-home keeps its old log path in
// the state file; JSON reports where the log is now.
func TestStatusJSONMovedLogFile(t *testing.T) {
	worktree := newSetupProject(t)
	state := &project.SetupState{Status: project.SetupFailed, LogFile: "/old/home/feature/" + project.SetupLogFile}
	writeSetupFixture(t, worktree, state, "output\n")

	var report statusReport
	out := runJSON(t, newStatusCmd)
	decodeJSON(t, out, &report)
	if len(report.Worktrees) != 1 || report.Worktrees[0].Setup == nil {
		t.Fatalf("want one entry with setup:\n%s", out)
	}
	if got := report.Worktrees[0].Setup.LogFile; !samePath(t, got, project.SetupLogPath(worktree)) {
		t.Errorf("log_file = %q, want the log in the worktree", got)
	}
}

func TestStatusJSONBrokenWorktree(t *testing.T) {
	worktree := newSetupProject(t)
	addBrokenWorktree(t, filepath.Dir(filepath.Dir(worktree)))

	out := runJSON(t, newStatusCmd)
	var report statusReport
	decodeJSON(t, out, &report)
	entries := map[string]statusEntry{}
	for _, entry := range report.Worktrees {
		entries[entry.Branch] = entry
	}

	alpha, ok := entries["alpha"]
	if !ok {
		t.Fatalf("missing alpha entry:\n%s", out)
	}
	if alpha.Dirty != nil || alpha.LastCommitAge != nil || alpha.Upstream != nil || alpha.Ahead != nil || alpha.Behind != nil ||
		!strings.HasPrefix(alpha.Error, "fatal:") {
		t.Errorf("broken entry should have null fields and a fatal error: %+v", alpha)
	}
	if feature := entries["feature"]; feature.Dirty == nil || *feature.Dirty || feature.Error != "" {
		t.Errorf("feature entry should be clean: %+v", feature)
	}
}

func TestStatusJSONEmpty(t *testing.T) {
	worktree := newSetupProject(t)
	if out, err := exec.Command("git", "worktree", "remove", "--force", worktree).CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove: %v\n%s", err, out)
	}

	out := runJSON(t, newStatusCmd)
	if !strings.Contains(out, `"worktrees": []`) {
		t.Errorf("empty project should give an empty array:\n%s", out)
	}
	var report statusReport
	decodeJSON(t, out, &report)
}

func TestRenderUpstream(t *testing.T) {
	tests := []struct {
		name string
		up   git.UpstreamStatus
		err  error
		want string
	}{
		{name: "even", up: git.UpstreamStatus{Upstream: "origin/feat"}, want: "="},
		{name: "ahead and behind", up: git.UpstreamStatus{Upstream: "origin/feat", Ahead: 2, Behind: 5}, want: "↑2 ↓5"},
		{name: "ahead only", up: git.UpstreamStatus{Upstream: "origin/feat", Ahead: 2}, want: "↑2"},
		{name: "behind only", up: git.UpstreamStatus{Upstream: "origin/feat", Behind: 5}, want: "↓5"},
		{name: "other upstream", up: git.UpstreamStatus{Upstream: "origin/main", Ahead: 2, Behind: 5}, want: "↑2 ↓5 (origin/main)"},
		{name: "even with other upstream", up: git.UpstreamStatus{Upstream: "origin/main"}, want: "= (origin/main)"},
		{name: "gone", up: git.UpstreamStatus{Upstream: "origin/feat", Gone: true}, want: "gone"},
		{name: "gone with other upstream", up: git.UpstreamStatus{Upstream: "origin/main", Gone: true}, want: "gone (origin/main)"},
		{name: "no upstream", want: "-"},
		{name: "lookup failed", err: errors.New("boom"), want: "error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Fprint drops the styling, as it does for a non-terminal.
			var plain bytes.Buffer
			_, _ = lipgloss.Fprint(&plain, renderUpstream("feat", tt.up, tt.err))
			if got := plain.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// upstreamProject builds a Bare layout project cloned from a source repo,
// with three worktrees: "tracked" tracks origin/tracked and is two commits
// ahead and one behind; "fresh" was created the way wtx add creates a new
// branch, so it tracks origin/main; "local" has no upstream.
func upstreamProject(t *testing.T) {
	t.Helper()
	t.Setenv("WTX_HOME", t.TempDir())
	t.Setenv("WTX_NO_DISK_WARN", "1")
	// Keep the user's config (branch.autoSetupMerge, signing) out of it.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	runGit := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	base := t.TempDir()
	src := filepath.Join(base, "src")
	runGit("init", "-b", "main", src)
	runGit("-C", src, "commit", "--allow-empty", "-m", "initial")
	runGit("-C", src, "branch", "tracked")

	root := filepath.Join(base, "proj")
	gitDir := filepath.Join(root, ".bare")
	runGit("clone", "--bare", src, gitDir)
	runGit("--git-dir", gitDir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	runGit("--git-dir", gitDir, "fetch", "origin")
	if err := os.WriteFile(filepath.Join(root, ".worktree.yml"), []byte("version: 1\ngit_dir: .bare\nworktree_dir: worktrees\nshared_dir: shared\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	worktree := func(name string) string { return filepath.Join(root, "worktrees", name) }
	runGit("--git-dir", gitDir, "worktree", "add", worktree("tracked"), "tracked")
	runGit("--git-dir", gitDir, "branch", "--set-upstream-to=origin/tracked", "tracked")
	runGit("--git-dir", gitDir, "worktree", "add", "-b", "fresh", worktree("fresh"), "origin/main")
	runGit("--git-dir", gitDir, "worktree", "add", "-b", "local", worktree("local"), "main")

	runGit("-C", worktree("tracked"), "commit", "--allow-empty", "-m", "local 1")
	runGit("-C", worktree("tracked"), "commit", "--allow-empty", "-m", "local 2")
	runGit("-C", src, "switch", "-q", "tracked")
	runGit("-C", src, "commit", "--allow-empty", "-m", "upstream")
	runGit("--git-dir", gitDir, "fetch", "origin")
	t.Chdir(root)
}

func TestStatusUpstream(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	upstreamProject(t)

	t.Run("table", func(t *testing.T) {
		gitLog := spyGit(t)
		out := captureUI(t)
		command := newStatusCmd()
		command.SetContext(context.Background())
		if err := runStatus(command, nil); err != nil {
			t.Fatal(err)
		}

		got := out.String()
		// Cells per branch, from table rows like "│ fresh │ worktrees/fresh │ … │".
		cells := map[string][]string{}
		var header []string
		for line := range strings.Lines(got) {
			fields := strings.Split(line, "│")
			if len(fields) < 3 {
				continue
			}
			for i := range fields {
				fields[i] = strings.TrimSpace(fields[i])
			}
			if fields[1] == "BRANCH" {
				header = fields
				continue
			}
			cells[fields[1]] = fields
		}
		column := slices.Index(header, "UPSTREAM")
		if column < 0 {
			t.Fatalf("missing UPSTREAM column:\n%s", got)
		}
		for branch, want := range map[string]string{"tracked": "↑2 ↓1", "fresh": "= (origin/main)", "local": "-"} {
			row, ok := cells[branch]
			if !ok {
				t.Errorf("missing %s row:\n%s", branch, got)
				continue
			}
			if row[column] != want {
				t.Errorf("%s UPSTREAM = %q, want %q", branch, row[column], want)
			}
		}
		if strings.Contains(got, "(origin/tracked)") {
			t.Errorf("an upstream of origin/<branch> should not be named:\n%s", got)
		}

		// Counts come from the last fetch; status never touches the network.
		calls := gitLog()
		if !strings.Contains(calls, "for-each-ref") {
			t.Fatalf("git spy saw no for-each-ref call:\n%s", calls)
		}
		for line := range strings.Lines(calls) {
			if slices.ContainsFunc(strings.Fields(line), func(arg string) bool {
				return slices.Contains([]string{"fetch", "pull", "push", "ls-remote"}, arg)
			}) {
				t.Errorf("status ran a network command: git %s", strings.TrimSpace(line))
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		var report statusReport
		out := runJSON(t, newStatusCmd)
		decodeJSON(t, out, &report)
		entries := map[string]statusEntry{}
		for _, entry := range report.Worktrees {
			entries[entry.Branch] = entry
		}

		check := func(branch, upstream string, ahead, behind int) {
			t.Helper()
			e := entries[branch]
			if e.Upstream == nil || *e.Upstream != upstream || e.Ahead == nil || *e.Ahead != ahead || e.Behind == nil || *e.Behind != behind {
				t.Errorf("%s entry = %+v, want upstream %q, ahead %d, behind %d", branch, e, upstream, ahead, behind)
			}
		}
		check("tracked", "origin/tracked", 2, 1)
		check("fresh", "origin/main", 0, 0)
		if local := entries["local"]; local.Upstream != nil || local.Ahead != nil || local.Behind != nil || local.Error != "" {
			t.Errorf("local entry should have null upstream fields and no error: %+v", local)
		}
		if !strings.Contains(out, `"upstream": null`) {
			t.Errorf("no upstream should be null:\n%s", out)
		}
	})
}
