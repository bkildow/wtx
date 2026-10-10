package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/git"
)

// quietRunner is a non-dry, quiet Runner for the checkout at root.
func quietRunner(root string) *git.Runner {
	r := git.NewRunner(filepath.Join(root, ".git"), false)
	r.Quiet = true
	return r
}

func TestReadCloneName(t *testing.T) {
	requireGit(t)
	root := filepath.Join(t.TempDir(), "myrepo")
	initRepo(t, root, false)
	runner := quietRunner(root)
	ctx := context.Background()

	got, err := ReadCloneName(ctx, runner, root)
	if err != nil {
		t.Fatal(err)
	}
	if got != (CloneName{Name: "myrepo", Source: NameFromDirectory}) {
		t.Errorf("unset wtx.name: got %+v", got)
	}

	runGit(t, "-C", root, "config", "--local", NameConfigKey, "other")
	got, err = ReadCloneName(ctx, runner, root)
	if err != nil {
		t.Fatal(err)
	}
	if got != (CloneName{Name: "other", Source: NameFromGitConfig}) {
		t.Errorf("wtx.name set: got %+v", got)
	}
	if !strings.Contains(got.Describe(), NameConfigKey) {
		t.Errorf("Describe() = %q", got.Describe())
	}

	runGit(t, "-C", root, "config", "--local", NameConfigKey, "../escape")
	if _, err := ReadCloneName(ctx, runner, root); err == nil || !strings.Contains(err.Error(), NameConfigKey) {
		t.Errorf("invalid wtx.name: err = %v", err)
	}
}

func TestReadCloneNameUnreadableGitDirFallsBack(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := ReadCloneName(context.Background(), quietRunner(root), root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "plain" || got.Source != NameFromDirectory {
		t.Errorf("got %+v", got)
	}
}

func TestPickCloneName(t *testing.T) {
	str := func(s string) *string { return &s }
	tests := []struct {
		name       string
		base       string
		override   *string
		current    string
		want       CloneName
		wantErrSub string
	}{
		{"override beats wtx.name", "repo", str("chosen"), "configured", CloneName{"chosen", NameFromOverride}, ""},
		{"wtx.name beats basename", "repo", nil, "configured", CloneName{"configured", NameFromGitConfig}, ""},
		{"basename when unset", "repo", nil, "", CloneName{"repo", NameFromDirectory}, ""},
		{"override wins over invalid wtx.name", "repo", str("good"), "bad/name", CloneName{"good", NameFromOverride}, ""},
		{"empty override is invalid", "repo", str(""), "", CloneName{}, "invalid clone name"},
		{"override with separator is invalid", "repo", str("../escape"), "", CloneName{}, "invalid clone name"},
		{"invalid wtx.name names git config", "repo", nil, "bad/name", CloneName{}, "git config " + NameConfigKey},
		{"invalid basename", "..", nil, "", CloneName{}, "invalid clone name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pickCloneName(tt.base, tt.override, tt.current)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPlanClone(t *testing.T) {
	requireGit(t)
	t.Setenv(WtxHomeEnv, t.TempDir())
	root := filepath.Join(t.TempDir(), "myrepo")
	initRepo(t, root, false)
	runner := quietRunner(root)
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.GitDir = ".git"

	plan, err := PlanClone(ctx, runner, root, &cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Name != (CloneName{Name: "myrepo", Source: NameFromDirectory}) || !plan.SetName() || plan.Current != "" {
		t.Errorf("fresh clone: got %+v", plan)
	}
	wantDir, _ := CloneDirFor("myrepo")
	if plan.CloneDir != wantDir {
		t.Errorf("CloneDir = %q, want %q", plan.CloneDir, wantDir)
	}
	if got := WorktreesPath(root, plan.Config); got != filepath.Join(wantDir, "worktrees") {
		t.Errorf("planned worktrees = %q", got)
	}
	if cfg.WorktreeDir != config.DefaultWorktreeDir {
		t.Errorf("PlanClone modified cfg: worktree_dir = %q", cfg.WorktreeDir)
	}

	runGit(t, "-C", root, "config", "--local", NameConfigKey, "myrepo")
	plan, err = PlanClone(ctx, runner, root, &cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Name.Source != NameFromGitConfig || plan.SetName() {
		t.Errorf("wtx.name recorded: got %+v", plan)
	}

	other := "other"
	plan, err = PlanClone(ctx, runner, root, &cfg, &other)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Name != (CloneName{Name: "other", Source: NameFromOverride}) || !plan.SetName() || plan.Current != "myrepo" {
		t.Errorf("override: got %+v", plan)
	}

	// A Clone dir owned by another root is taken.
	taken, _ := CloneDirFor("taken")
	if err := os.MkdirAll(taken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarker(taken, t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	name := "taken"
	if _, err := PlanClone(ctx, runner, root, &cfg, &name); !errors.Is(err, ErrCloneDirTaken) {
		t.Errorf("taken dir: err = %v, want ErrCloneDirTaken", err)
	}

	// Explicit paths leave nothing to plan, before git is read.
	explicit := cfg
	explicit.SetWorktreeDir("wt")
	explicit.SetSharedDir("sh")
	if _, err := PlanClone(ctx, git.NewRunner(filepath.Join(t.TempDir(), "missing"), false), root, &explicit, nil); !errors.Is(err, ErrNoCloneDir) {
		t.Errorf("explicit paths: err = %v, want ErrNoCloneDir", err)
	}
}
