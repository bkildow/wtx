package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bkildow/wtx/cmd"
	"github.com/bkildow/wtx/internal/config"
	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"wt": func() {
			if err := cmd.Execute(); err != nil {
				os.Exit(1)
			}
		},
	})
}

func TestScripts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E tests in short mode")
	}

	testscript.Run(t, testscript.Params{
		Dir:                 "testdata",
		RequireExplicitExec: true,
		Setup:               setupEnv,
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"setup-repo":    cmdSetupRepo,
			"setup-project": cmdSetupProject,
		},
	})
}

// setupEnv configures the environment for each test script.
func setupEnv(env *testscript.Env) error {
	env.Setenv("NO_COLOR", "1")
	env.Setenv("TERM", "dumb")
	env.Setenv("GIT_AUTHOR_NAME", "Test")
	env.Setenv("GIT_AUTHOR_EMAIL", "test@test.com")
	env.Setenv("GIT_COMMITTER_NAME", "Test")
	env.Setenv("GIT_COMMITTER_EMAIL", "test@test.com")
	env.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	env.Setenv("GIT_PAGER", "cat")
	// A low-disk CI runner would otherwise inject a warning into command output.
	env.Setenv("WT_NO_DISK_WARN", "1")
	return nil
}

// gitEnv returns the environment for git commands run from the setup helpers.
// These run outside the testscript environment, so they do not inherit the
// identity set in setupEnv and would otherwise depend on the caller's global
// git config -- which is absent on CI runners, where commit fails outright.
func gitEnv() []string {
	return append(
		os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

// cmdSetupRepo creates a git repository at $WORK/remote with an initial
// commit. Additional branch names can be passed as arguments.
func cmdSetupRepo(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("setup-repo does not support negation")
	}

	repoDir := filepath.Join(ts.Getenv("WORK"), "remote")
	gitRun := func(gitArgs ...string) {
		c := exec.Command("git", gitArgs...)
		c.Dir = repoDir
		c.Env = gitEnv()
		out, err := c.CombinedOutput()
		if err != nil {
			ts.Fatalf("git %v: %v\n%s", gitArgs, err, out)
		}
	}

	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		ts.Fatalf("mkdir remote: %v", err)
	}

	// Pin the initial branch name. Without -b the branch comes from the
	// ambient init.defaultBranch, so the scripts (which reference master
	// explicitly) fail for anyone whose git defaults to main.
	gitRun("init", "-b", "master")

	readme := filepath.Join(repoDir, "README.md")
	if err := os.WriteFile(readme, []byte("# Test Repo\n"), 0o644); err != nil {
		ts.Fatalf("write README: %v", err)
	}
	gitRun("add", ".")
	gitRun("commit", "-m", "initial commit")

	for _, branch := range args {
		gitRun("branch", branch)
	}
}

// cmdSetupProject creates a fully-formed wt project at $WORK/project
// by cloning bare from $WORK/remote, configuring the fetch refspec,
// and writing .worktree.yml + scaffold directories.
func cmdSetupProject(ts *testscript.TestScript, neg bool, args []string) { //nolint:unparam // signature required by testscript.Cmds
	if neg {
		ts.Fatalf("setup-project does not support negation")
	}

	work := ts.Getenv("WORK")
	remoteDir := filepath.Join(work, "remote")
	projectDir := filepath.Join(work, "project")
	bareDir := filepath.Join(projectDir, config.DefaultGitDir)

	c := exec.Command("git", "clone", "--bare", remoteDir, bareDir)
	c.Env = gitEnv()
	out, err := c.CombinedOutput()
	if err != nil {
		ts.Fatalf("git clone --bare: %v\n%s", err, out)
	}

	gitRun := func(gitArgs ...string) {
		c := exec.Command("git", append([]string{"--git-dir", bareDir}, gitArgs...)...)
		c.Env = gitEnv()
		out, err := c.CombinedOutput()
		if err != nil {
			ts.Fatalf("git %v: %v\n%s", gitArgs, err, out)
		}
	}

	gitRun("config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	gitRun("fetch", "origin")

	for _, dir := range []string{
		filepath.Join(projectDir, "shared", "copy"),
		filepath.Join(projectDir, "shared", "symlink"),
		filepath.Join(projectDir, "worktrees"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			ts.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	cfg := "version: 1\ngit_dir: " + config.DefaultGitDir + "\nworktree_dir: " + config.DefaultWorktreeDir + "\nshared_dir: " + config.DefaultSharedDir + "\n"
	cfgPath := filepath.Join(projectDir, config.ConfigFileName)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		ts.Fatalf("write config: %v", err)
	}
}
