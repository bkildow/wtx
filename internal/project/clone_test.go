package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
