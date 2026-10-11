package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

// Captured from herdr 0.9.0, paths shortened.
const (
	herdrCreatedPayload = `{"event":"worktree_created","data":{"type":"worktree_created","workspace":{"workspace_id":"wX","worktree":{"repo_key":"/proj/.git","repo_name":"proj","repo_root":"/proj","checkout_path":"/herdr/proj/feat","is_linked_worktree":true}},"worktree":{"path":"/herdr/proj/feat","branch":"feat","is_bare":false,"is_linked_worktree":true,"open_workspace_id":"wX","label":"proj"}}}`
	herdrRemovedPayload = `{"event":"worktree_removed","data":{"type":"worktree_removed","workspace_id":"wX","workspace":{"workspace_id":"wX","worktree":{"repo_key":"/proj/.bare","repo_name":"proj","repo_root":"/proj/worktrees/main","checkout_path":"/herdr/proj/feat","is_linked_worktree":true}},"worktree":{"path":"/herdr/proj/feat","branch":"feat","is_bare":false,"is_linked_worktree":true,"label":"proj"},"forced":false}}`
)

func TestParseHerdrEvent(t *testing.T) {
	ev, err := parseHerdrEvent(herdrCreatedPayload, herdrEventCreated)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Data.Worktree.Path != "/herdr/proj/feat" || ev.Data.Worktree.Branch != "feat" {
		t.Errorf("worktree = %+v", ev.Data.Worktree)
	}
	if ev.Data.Workspace.Worktree.RepoKey != "/proj/.git" {
		t.Errorf("repo_key = %q", ev.Data.Workspace.Worktree.RepoKey)
	}

	if _, err := parseHerdrEvent(herdrRemovedPayload, herdrEventRemoved); err != nil {
		t.Errorf("removed payload: %v", err)
	}
	if _, err := parseHerdrEvent(herdrRemovedPayload, herdrEventCreated); err == nil {
		t.Error("want an error for the wrong event")
	}
	if _, err := parseHerdrEvent("", herdrEventCreated); err == nil {
		t.Error("want an error when the variable is unset")
	}
	if _, err := parseHerdrEvent("{nope", herdrEventCreated); err == nil {
		t.Error("want an error for invalid JSON")
	}
}

func TestHerdrProjectRoot(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".worktree.yml"), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plain := t.TempDir()

	for _, tt := range []struct {
		name, repoKey, want string
		ok                  bool
	}{
		{"bare layout", filepath.Join(project, ".bare"), project, true},
		{"checkout layout", filepath.Join(project, ".git"), project, true},
		{"not a wtx project", filepath.Join(plain, ".git"), "", false},
		{"no repo key", "", "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := herdrProjectRoot(tt.repoKey)
			if ok != tt.ok || (ok && got != tt.want) {
				t.Errorf("herdrProjectRoot(%q) = %q, %v; want %q, %v", tt.repoKey, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestHerdrStatePath(t *testing.T) {
	t.Setenv(herdrStateDirEnv, "/state")
	a := herdrStatePath("setup", "/herdr/proj/feat")
	if !strings.HasPrefix(a, filepath.FromSlash("/state/setup/feat-")) {
		t.Errorf("path = %q", a)
	}
	if b := herdrStatePath("setup", "/herdr/other/feat"); a == b {
		t.Errorf("checkouts with the same base name share %q", a)
	}
}

func TestFindWorktreeByPath(t *testing.T) {
	dir := t.TempDir()
	wts := []git.WorktreeInfo{{Path: filepath.Join(dir, "a"), Branch: "a"}, {Path: filepath.Join(dir, "b"), Branch: "b"}}
	if wt, ok := findWorktreeByPath(wts, filepath.Join(dir, "b")); !ok || wt.Branch != "b" {
		t.Errorf("got %+v, %v", wt, ok)
	}
	if _, ok := findWorktreeByPath(wts, filepath.Join(dir, "c")); ok {
		t.Error("found a worktree that is not listed")
	}
}

func TestHerdrManifestCommands(t *testing.T) {
	m := herdrManifest("/bin/wtx")
	for _, want := range []string{
		`id = "wtx"`,
		`on = "worktree.created"`,
		`command = ["/bin/wtx", "herdr", "worktree-created"]`,
		`command = ["/bin/wtx", "herdr", "worktree-removed"]`,
		`command = ["/bin/wtx", "herdr", "open-remove"]`,
		`command = ["/bin/wtx", "herdr", "remove"]`,
	} {
		if !strings.Contains(m, want) {
			t.Errorf("manifest is missing %s\n%s", want, m)
		}
	}
}

// runRemovedHook runs the worktree.removed handler for a checkout of a
// project whose .worktree.yml is yml, and returns its output.
func runRemovedHook(t *testing.T, yml string, beforeRun func(checkout string)) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".worktree.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(t.TempDir(), "feat")
	t.Setenv(herdrStateDirEnv, t.TempDir())
	t.Setenv(herdrBinEnv, "true") // swallow the notification
	payload := strings.NewReplacer("/proj/.bare", filepath.Join(root, ".bare"), "/herdr/proj/feat", checkout).Replace(herdrRemovedPayload)
	t.Setenv(herdrEventEnv, payload)
	if beforeRun != nil {
		beforeRun(checkout)
	}

	var out bytes.Buffer
	orig := ui.Output
	ui.Output = &out
	t.Cleanup(func() { ui.Output = orig })
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	if err := runHerdrWorktreeRemoved(cmd, nil); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestHerdrWorktreeRemoved(t *testing.T) {
	const withTeardown = "version: 1\nteardown:\n  - docker compose down\n"

	t.Run("warns about skipped teardown", func(t *testing.T) {
		out := runRemovedHook(t, withTeardown, nil)
		if !strings.Contains(out, "skipped 1 hook(s)") || !strings.Contains(out, "docker compose down") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("quiet after the remove action", func(t *testing.T) {
		var marker string
		out := runRemovedHook(t, withTeardown, func(checkout string) {
			if err := writeHerdrState("removed", checkout, "feat"); err != nil {
				t.Fatal(err)
			}
			marker = herdrStatePath("removed", checkout)
		})
		if strings.Contains(out, "skipped") || !strings.Contains(out, "teardown ran first") {
			t.Errorf("output = %q", out)
		}
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Errorf("marker not cleaned up: %v", err)
		}
	})

	t.Run("silent without teardown hooks", func(t *testing.T) {
		if out := runRemovedHook(t, "version: 1\n", nil); out != "" {
			t.Errorf("output = %q", out)
		}
	})
}

// fakeConfirm answers every Confirm with answer and records the questions.
type fakeConfirm struct {
	ui.InteractivePrompter
	answer    bool
	questions []string
}

func (f *fakeConfirm) Confirm(title string) (bool, error) {
	f.questions = append(f.questions, title)
	return f.answer, nil
}

func TestHerdrRemoveRechecksDirtyAfterTeardown(t *testing.T) {
	for _, tt := range []struct {
		name, teardown string
		answer         bool
		wantAsked      bool
		wantHerdr      string // herdr argv, "" when herdr must not run
	}{
		{"clean teardown", "'true'", false, false, "worktree remove --workspace wX"},
		{"teardown leaves a file, user forces", "touch leftover.txt", true, true, "worktree remove --workspace wX --force"},
		{"teardown leaves a file, user cancels", "touch leftover.txt", false, true, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WTX_HOME", t.TempDir())
			root := t.TempDir()
			runGit := func(args ...string) {
				t.Helper()
				if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			runGit("init", "-q", root)
			yml := "git_dir: .git\nworktree_dir: worktrees\nshared_dir: shared\nteardown:\n  - " + tt.teardown + "\n"
			if err := os.WriteFile(filepath.Join(root, ".worktree.yml"), []byte(yml), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit("-C", root, "add", ".worktree.yml")
			runGit("-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial")
			checkout := filepath.Join(t.TempDir(), "feat")
			runGit("-C", root, "worktree", "add", "-q", "-b", "feat", checkout)

			// herdr stand-in that records its arguments.
			bin := t.TempDir()
			argsFile := filepath.Join(bin, "args")
			if err := os.WriteFile(filepath.Join(bin, "herdr"), []byte("#!/bin/sh\necho \"$*\" > "+argsFile+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv(herdrBinEnv, filepath.Join(bin, "herdr"))
			t.Setenv(herdrStateDirEnv, t.TempDir())
			t.Setenv(herdrContextEnv, `{"workspace_id":"wX","worktree":{"repo_key":"`+filepath.Join(root, ".git")+`","checkout_path":"`+checkout+`","is_linked_worktree":true}}`)
			orig := ui.Output
			ui.Output = &bytes.Buffer{}
			t.Cleanup(func() { ui.Output = orig })

			prompter := &fakeConfirm{answer: tt.answer}
			if err := herdrRemove(context.Background(), prompter); err != nil {
				t.Fatal(err)
			}

			asked := len(prompter.questions) == 1 && strings.HasPrefix(prompter.questions[0], "Teardown left")
			if asked != tt.wantAsked || len(prompter.questions) > 1 {
				t.Errorf("questions = %q, want asked=%v", prompter.questions, tt.wantAsked)
			}
			got, err := os.ReadFile(argsFile)
			if tt.wantHerdr == "" {
				if err == nil {
					t.Errorf("herdr ran with %q after the user cancelled", got)
				}
				return
			}
			if strings.TrimSpace(string(got)) != tt.wantHerdr {
				t.Errorf("herdr args = %q, want %q", got, tt.wantHerdr)
			}
		})
	}
}
