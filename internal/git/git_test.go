package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/ui"
)

func TestBatchEnv(t *testing.T) {
	env := batchEnv()

	var hasTerminalPrompt, hasSSH bool
	for _, v := range env {
		if v == "GIT_TERMINAL_PROMPT=0" {
			hasTerminalPrompt = true
		}
		if strings.HasPrefix(v, "GIT_SSH_COMMAND=") && strings.Contains(v, "BatchMode=yes") {
			hasSSH = true
		}
	}
	if !hasTerminalPrompt {
		t.Error("batchEnv missing GIT_TERMINAL_PROMPT=0")
	}
	if !hasSSH {
		t.Error("batchEnv missing GIT_SSH_COMMAND with BatchMode=yes")
	}
}

func TestRunnerBatchMode(t *testing.T) {
	// BatchMode defaults to false.
	r := NewRunner("/tmp/fake", false)
	if r.BatchMode {
		t.Error("BatchMode should default to false")
	}

	// Setting BatchMode should not affect DryRun.
	r.BatchMode = true
	if r.DryRun {
		t.Error("setting BatchMode should not affect DryRun")
	}
}

func TestParseRemoteBranches(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []string
	}{
		{
			name:   "typical output",
			input:  "  origin/main\n  origin/develop\n  origin/feature/login",
			expect: []string{"main", "develop", "feature/login"},
		},
		{
			name:   "with HEAD pointer",
			input:  "  origin/HEAD -> origin/main\n  origin/main\n  origin/develop",
			expect: []string{"main", "develop"},
		},
		{
			name:   "empty output",
			input:  "",
			expect: nil,
		},
		{
			name:   "single branch",
			input:  "  origin/main",
			expect: []string{"main"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRemoteBranches(tt.input)
			if len(got) != len(tt.expect) {
				t.Fatalf("got %v, want %v", got, tt.expect)
			}
			for i := range got {
				if got[i] != tt.expect[i] {
					t.Errorf("branch[%d] = %q, want %q", i, got[i], tt.expect[i])
				}
			}
		})
	}
}

func TestParseWorktreeList(t *testing.T) {
	input := `worktree /home/user/project/.bare
HEAD abc1234567890
branch refs/heads/main
bare

worktree /home/user/project/worktrees/develop
HEAD def4567890123
branch refs/heads/develop

`

	got := parseWorktreeList(input)
	if len(got) != 2 {
		t.Fatalf("got %d worktrees, want 2", len(got))
	}

	if got[0].Path != "/home/user/project/.bare" {
		t.Errorf("worktree[0].Path = %q", got[0].Path)
	}
	if got[0].Branch != "main" {
		t.Errorf("worktree[0].Branch = %q, want %q", got[0].Branch, "main")
	}
	if !got[0].Bare {
		t.Error("worktree[0].Bare should be true")
	}

	if got[1].Path != "/home/user/project/worktrees/develop" {
		t.Errorf("worktree[1].Path = %q", got[1].Path)
	}
	if got[1].Branch != "develop" {
		t.Errorf("worktree[1].Branch = %q, want %q", got[1].Branch, "develop")
	}
	if got[1].Bare {
		t.Error("worktree[1].Bare should be false")
	}
}

// TestDryRunMode covers the state-changing commands, which dry-run skips.
// Read-only queries are deliberately absent here: they must execute under
// dry-run, so they are covered by TestDryRunExecutesQueries against a real
// repository. A fake git dir would pass for the wrong reason.
func TestDryRunMode(t *testing.T) {
	// Redirect UI output to discard
	ui.Output = os.Stderr

	runner := NewRunner("/nonexistent", true)
	ctx := context.Background()

	// These should not error because dry-run skips execution
	out, err := runner.Run(ctx, "status")
	if err != nil {
		t.Errorf("dry-run Run returned error: %v", err)
	}
	if out != "" {
		t.Errorf("dry-run Run returned output: %q", out)
	}

	if err := runner.CloneBare(ctx, "https://example.com/repo.git", "/tmp/dest"); err != nil {
		t.Errorf("dry-run CloneBare returned error: %v", err)
	}

	if err := runner.ConfigureRemoteFetch(ctx); err != nil {
		t.Errorf("dry-run ConfigureRemoteFetch returned error: %v", err)
	}

	if err := runner.Fetch(ctx, "origin"); err != nil {
		t.Errorf("dry-run Fetch returned error: %v", err)
	}

	// WorktreeAddNew
	if err := runner.WorktreeAddNew(ctx, "/tmp/wt", "feature-x", "main"); err != nil {
		t.Errorf("dry-run WorktreeAddNew returned error: %v", err)
	}

	// WorktreeRemove
	if err := runner.WorktreeRemove(ctx, "/tmp/wt", false); err != nil {
		t.Errorf("dry-run WorktreeRemove returned error: %v", err)
	}
	if err := runner.WorktreeRemove(ctx, "/tmp/wt", true); err != nil {
		t.Errorf("dry-run WorktreeRemove (force) returned error: %v", err)
	}

	// BranchDelete
	if err := runner.BranchDelete(ctx, "old-branch", false); err != nil {
		t.Errorf("dry-run BranchDelete returned error: %v", err)
	}
	if err := runner.BranchDelete(ctx, "old-branch", true); err != nil {
		t.Errorf("dry-run BranchDelete (force) returned error: %v", err)
	}

	// FetchAll
	if err := runner.FetchAll(ctx); err != nil {
		t.Errorf("dry-run FetchAll returned error: %v", err)
	}

	// WorktreePrune
	if err := runner.WorktreePrune(ctx); err != nil {
		t.Errorf("dry-run WorktreePrune returned error: %v", err)
	}

	// Pull
	if err := runner.Pull(ctx, "/tmp/wt"); err != nil {
		t.Errorf("dry-run Pull returned error: %v", err)
	}

	// PullRebase
	if err := runner.PullRebase(ctx, "/tmp/wt"); err != nil {
		t.Errorf("dry-run PullRebase returned error: %v", err)
	}
}

// TestDryRunExecutesQueries guards the regression where --dry-run stubbed out
// read-only queries too. Commands like `wtx prune` decide what to touch by
// walking WorktreeList and asking IsBranchMerged; when those returned empty
// and true, `wtx prune --dry-run` reported "No merged worktrees to prune" in
// every repository, no matter how many were actually merged.
func TestDryRunExecutesQueries(t *testing.T) {
	ui.Output = os.Stderr

	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(
			os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f.txt")
	run("commit", "-qm", "initial")
	// merged points at main, so it is an ancestor; unmerged carries its own commit.
	run("branch", "merged")
	run("checkout", "-qb", "unmerged")
	if err := os.WriteFile(filepath.Join(repo, "g.txt"), []byte("yo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "g.txt")
	run("commit", "-qm", "second")
	run("checkout", "-q", "main")

	runner := NewRunner(filepath.Join(repo, ".git"), true)
	ctx := context.Background()

	worktrees, err := runner.WorktreeList(ctx)
	if err != nil {
		t.Fatalf("dry-run WorktreeList returned error: %v", err)
	}
	if len(worktrees) == 0 {
		t.Error("dry-run WorktreeList returned nothing; queries must execute under dry-run")
	}

	merged, err := runner.IsBranchMerged(ctx, "merged", "main")
	if err != nil {
		t.Fatalf("dry-run IsBranchMerged returned error: %v", err)
	}
	if !merged {
		t.Error("dry-run IsBranchMerged(merged, main) = false, want true")
	}

	// The half that the old unconditional `return true` got wrong.
	unmerged, err := runner.IsBranchMerged(ctx, "unmerged", "main")
	if err != nil {
		t.Fatalf("dry-run IsBranchMerged returned error: %v", err)
	}
	if unmerged {
		t.Error("dry-run IsBranchMerged(unmerged, main) = true, want false")
	}

	hasLocal, err := runner.HasLocalBranch(ctx, "merged")
	if err != nil {
		t.Fatalf("dry-run HasLocalBranch returned error: %v", err)
	}
	if !hasLocal {
		t.Error("dry-run HasLocalBranch(merged) = false, want true")
	}
	absent, err := runner.HasLocalBranch(ctx, "no-such-branch")
	if err != nil {
		t.Fatalf("dry-run HasLocalBranch returned error: %v", err)
	}
	if absent {
		t.Error("dry-run HasLocalBranch(no-such-branch) = true, want false")
	}

	dirty, err := runner.IsWorktreeDirty(ctx, repo)
	if err != nil {
		t.Fatalf("dry-run IsWorktreeDirty returned error: %v", err)
	}
	if dirty {
		t.Error("dry-run IsWorktreeDirty = true on a clean tree")
	}
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = runner.IsWorktreeDirty(ctx, repo)
	if err != nil {
		t.Fatalf("dry-run IsWorktreeDirty returned error: %v", err)
	}
	if !dirty {
		t.Error("dry-run IsWorktreeDirty = false on a dirty tree")
	}

	age, err := runner.GetLastCommitAge(ctx, repo)
	if err != nil {
		t.Fatalf("dry-run GetLastCommitAge returned error: %v", err)
	}
	if age == "" || age == "unknown" {
		t.Errorf("dry-run GetLastCommitAge = %q, want a real relative date", age)
	}
}

func TestParseDirtyStatus(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{
			name:   "empty string",
			input:  "",
			expect: false,
		},
		{
			name:   "whitespace only",
			input:  "   \n\t\n  ",
			expect: false,
		},
		{
			name:   "single modified file",
			input:  " M cmd/root.go\n",
			expect: true,
		},
		{
			name:   "multiple files",
			input:  " M cmd/root.go\n?? newfile.txt\nA  added.go\n",
			expect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDirtyStatus(tt.input)
			if got != tt.expect {
				t.Errorf("parseDirtyStatus(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestParseDefaultBranch(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "full ref",
			input:  "refs/remotes/origin/main",
			expect: "main",
		},
		{
			name:   "with trailing newline",
			input:  "refs/remotes/origin/develop\n",
			expect: "develop",
		},
		{
			name:   "already short",
			input:  "main",
			expect: "main",
		},
		{
			name:   "empty",
			input:  "",
			expect: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDefaultBranch(tt.input)
			if got != tt.expect {
				t.Errorf("parseDefaultBranch(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestParseBranchList(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []string
	}{
		{
			name:   "typical output",
			input:  "  main\n  develop\n  feature/login",
			expect: []string{"main", "develop", "feature/login"},
		},
		{
			name:   "with current branch marker",
			input:  "* main\n  develop\n  feature/login",
			expect: []string{"main", "develop", "feature/login"},
		},
		{
			name:   "empty",
			input:  "",
			expect: nil,
		},
		{
			name:   "single branch",
			input:  "* main",
			expect: []string{"main"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBranchList(tt.input)
			if len(got) != len(tt.expect) {
				t.Fatalf("parseBranchList(%q) got %v, want %v", tt.input, got, tt.expect)
			}
			for i := range got {
				if got[i] != tt.expect[i] {
					t.Errorf("branch[%d] = %q, want %q", i, got[i], tt.expect[i])
				}
			}
		})
	}
}

func TestParseBehindCount(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect int
	}{
		{
			name:   "zero",
			input:  "0\n",
			expect: 0,
		},
		{
			name:   "positive",
			input:  "5\n",
			expect: 5,
		},
		{
			name:   "invalid",
			input:  "not a number",
			expect: 0,
		},
		{
			name:   "empty",
			input:  "",
			expect: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseBehindCount(tt.input)
			if got != tt.expect {
				t.Errorf("parseBehindCount(%q) = %d, want %d", tt.input, got, tt.expect)
			}
		})
	}
}

func TestParseGitVersion(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		expect  [3]int
		wantErr bool
	}{
		{
			name:   "standard",
			input:  "git version 2.52.0\n",
			expect: [3]int{2, 52, 0},
		},
		{
			name:   "apple suffix",
			input:  "git version 2.39.5 (Apple Git-154)\n",
			expect: [3]int{2, 39, 5},
		},
		{
			name:   "rc suffix",
			input:  "git version 2.48.0.rc1\n",
			expect: [3]int{2, 48, 0},
		},
		{
			name:   "two-part version",
			input:  "git version 2.47\n",
			expect: [3]int{2, 47, 0},
		},
		{
			name:    "missing version",
			input:   "git version\n",
			wantErr: true,
		},
		{
			name:    "garbage",
			input:   "not a git output",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGitVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseGitVersion(%q) err = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.expect {
				t.Errorf("parseGitVersion(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestSupportsRelativePaths(t *testing.T) {
	tests := []struct {
		name   string
		v      [3]int
		expect bool
	}{
		{"2.47.9", [3]int{2, 47, 9}, false},
		{"2.48.0", [3]int{2, 48, 0}, true},
		{"2.48.5", [3]int{2, 48, 5}, true},
		{"2.52.0", [3]int{2, 52, 0}, true},
		{"3.0.0", [3]int{3, 0, 0}, true},
		{"1.9.0", [3]int{1, 9, 0}, false},
		{"zero", [3]int{0, 0, 0}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := supportsRelativePaths(tt.v); got != tt.expect {
				t.Errorf("supportsRelativePaths(%v) = %v, want %v", tt.v, got, tt.expect)
			}
		})
	}
}

func TestIntegrationCloneAndWorktree(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Check git is available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// Create a source repo to clone from
	srcDir := t.TempDir()
	cmds := [][]string{
		{"git", "init", srcDir},
		{"git", "-C", srcDir, "config", "user.email", "test@test.com"},
		{"git", "-C", srcDir, "config", "user.name", "Test"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v failed: %v\n%s", args, err, out)
		}
	}

	// Create a file and commit
	if err := os.WriteFile(filepath.Join(srcDir, "README.md"), []byte("# Test"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmds = [][]string{
		{"git", "-C", srcDir, "add", "."},
		{"git", "-C", srcDir, "commit", "-m", "initial"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("cmd %v failed: %v\n%s", args, err, out)
		}
	}

	// Clone bare
	projectDir := t.TempDir()
	bareDir := filepath.Join(projectDir, ".bare")

	runner := NewRunner(bareDir, false)
	ctx := context.Background()

	if err := runner.CloneBare(ctx, srcDir, bareDir); err != nil {
		t.Fatalf("CloneBare: %v", err)
	}

	// Configure remote fetch
	if err := runner.ConfigureRemoteFetch(ctx); err != nil {
		t.Fatalf("ConfigureRemoteFetch: %v", err)
	}

	// Fetch
	if err := runner.Fetch(ctx, "origin"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	// List remote branches
	branches, err := runner.ListRemoteBranches(ctx)
	if err != nil {
		t.Fatalf("ListRemoteBranches: %v", err)
	}

	found := false
	for _, b := range branches {
		if b == "main" || b == "master" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected main or master in branches: %v", branches)
	}

	// Add a worktree
	defaultBranch := branches[0]
	wtPath := filepath.Join(projectDir, "worktrees", defaultBranch)
	if err := runner.WorktreeAdd(ctx, wtPath, defaultBranch); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}

	// Verify the worktree directory exists
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		t.Error("worktree directory was not created")
	}

	// Verify the .git file uses a relative path
	gitFile := filepath.Join(wtPath, ".git")
	gitFileContent, err := os.ReadFile(gitFile)
	if err != nil {
		t.Fatalf("reading .git file: %v", err)
	}
	gitdirLine := strings.TrimSpace(string(gitFileContent))
	if !strings.HasPrefix(gitdirLine, "gitdir: ../") {
		t.Errorf(".git file should use relative path, got: %s", gitdirLine)
	}

	// Verify extensions.worktreeConfig is enabled on the bare common dir.
	bareCfg, err := os.ReadFile(filepath.Join(bareDir, "config"))
	if err != nil {
		t.Fatalf("reading bare config: %v", err)
	}
	if !strings.Contains(string(bareCfg), "worktreeConfig = true") {
		t.Errorf("bare config missing extensions.worktreeConfig = true:\n%s", bareCfg)
	}

	// Verify the worktree has its own config.worktree with core.bare = false.
	gitdirRel := strings.TrimSpace(strings.TrimPrefix(gitdirLine, "gitdir:"))
	worktreeGitDir := gitdirRel
	if !filepath.IsAbs(worktreeGitDir) {
		worktreeGitDir = filepath.Join(wtPath, worktreeGitDir)
	}
	wtCfg, err := os.ReadFile(filepath.Join(worktreeGitDir, "config.worktree"))
	if err != nil {
		t.Fatalf("reading worktree config.worktree: %v", err)
	}
	if !strings.Contains(string(wtCfg), "bare = false") {
		t.Errorf("config.worktree missing core.bare = false:\n%s", wtCfg)
	}

	// Auto-discovery should work from inside the worktree (the regression we
	// are guarding against on git 2.52+).
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = wtPath
	if out, err := statusCmd.CombinedOutput(); err != nil {
		t.Errorf("git status inside worktree should succeed: %v\n%s", err, out)
	}

	// WorktreeAdd should set up upstream tracking for a branch that exists on
	// origin, so @{upstream}-dependent features (behind-count, pull) work.
	upstreamCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	upstreamCmd.Dir = wtPath
	out, err := upstreamCmd.CombinedOutput()
	if err != nil {
		t.Errorf("worktree branch should have an upstream: %v\n%s", err, out)
	}
	if got, want := strings.TrimSpace(string(out)), "origin/"+defaultBranch; got != want {
		t.Errorf("upstream = %q, want %q", got, want)
	}

	// List worktrees
	worktrees, err := runner.WorktreeList(ctx)
	if err != nil {
		t.Fatalf("WorktreeList: %v", err)
	}

	if len(worktrees) < 2 {
		t.Errorf("expected at least 2 worktrees (bare + added), got %d", len(worktrees))
	}
}
