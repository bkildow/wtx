package doctor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestHookMigrationAndSettingsLinks(t *testing.T) {
	root, gitDir, _ := fixture(t, false)
	bin := resolved(t.TempDir())
	write(t, filepath.Join(bin, "wtx"), "#!/bin/sh\nexit 99\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	wt := filepath.Join(root, "worktrees", "feature")
	gitRun(t, "--git-dir", gitDir, "worktree", "add", "-b", "feature", wt)
	settings := filepath.Join(root, "shared", "symlink", ".claude", "settings.local.json")
	input := `{"permissions":{"allow":["custom"]},"large":9007199254740993,"hooks":{"WorktreeCreate":[{"hooks":[{"type":"command","command":"wt claude hook-worktree-create","timeout":17},{"type":"command","command":"echo custom && wt status"}]}],"WorktreeRemove":[{"hooks":[{"type":"command","command":"` + filepath.Join(bin, "wt") + ` claude hook-worktree-remove"}]}]}}`
	write(t, settings, input, 0o640)
	if err := os.MkdirAll(filepath.Join(wt, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(settings, filepath.Join(wt, ".claude", "settings.local.json")); err != nil {
		t.Fatal(err)
	}
	before := files(t, root)
	r := Run(context.Background(), Options{StartDir: root, Fix: true, DryRun: true})
	if !reflect.DeepEqual(before, files(t, root)) {
		t.Fatal("hook preview wrote files")
	}
	count := 0
	for _, repair := range r.Repairs {
		if repair.ID == "claude.hooks" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate linked repairs: %+v", r.Repairs)
	}
	r = Run(context.Background(), Options{StartDir: root, Fix: true})
	if r.Unsuccessful(false) {
		t.Fatalf("hook repair failed: %+v", r)
	}
	if f := finding(r, "shared.symlink", ""); f != nil {
		t.Fatalf("backup was mistaken for a managed shared file: %+v", f)
	}
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"wtx claude hook-worktree-create", filepath.Join(bin, "wtx") + " claude hook-worktree-remove", "9007199254740993", "permissions"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("lost %s in %s", want, data)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	custom := decoded["hooks"].(map[string]any)["WorktreeCreate"].([]any)[0].(map[string]any)["hooks"].([]any)[1].(map[string]any)["command"]
	if custom != "echo custom && wt status" {
		t.Fatalf("custom hook changed: %v", custom)
	}
	info, err := os.Lstat(filepath.Join(wt, ".claude", "settings.local.json"))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("settings link replaced")
	}
	after := files(t, root)
	r = Run(context.Background(), Options{StartDir: root, Fix: true})
	if len(r.Repairs) != 0 || !reflect.DeepEqual(after, files(t, root)) {
		t.Fatal("hook repair not idempotent")
	}
	// A changed link is a changed repair input, even if the old target survives.
	write(t, settings, input, 0o640)
	s := inspect(context.Background(), Options{StartDir: root})
	other := filepath.Join(root, "other-settings.json")
	write(t, other, "{}", 0o600)
	// The shared target is inspected directly first; replacing that target is guarded.
	if err := os.Rename(other, settings); err != nil {
		t.Fatal(err)
	}
	for _, repair := range s.repairs {
		if repair.id == "claude.hooks" {
			if _, err := repair.apply(context.Background()); err == nil {
				t.Fatal("accepted changed settings")
			}
		}
	}
}

func TestHookExternalMalformedAndMissingExecutables(t *testing.T) {
	root, _, _ := fixture(t, false)
	external := filepath.Join(resolved(t.TempDir()), "settings.json")
	write(t, external, `{"hooks":{"WorktreeCreate":[{"hooks":[{"type":"command","command":"wt claude hook-worktree-create"}]}]}}`, 0o600)
	path := filepath.Join(root, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, path); err != nil {
		t.Fatal(err)
	}
	before := files(t, filepath.Dir(external))
	r := Run(context.Background(), Options{StartDir: root, Fix: true})
	if f := finding(r, "claude.hooks", path); f == nil || f.Repairable || f.Severity != "warn" {
		t.Fatalf("external settings: %+v", f)
	}
	if !reflect.DeepEqual(before, files(t, filepath.Dir(external))) {
		t.Fatal("external settings modified")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{`, `null`, `{"hooks":[]}`, `{"hooks":{"WorktreeCreate":{}}}`, `{"hooks":{"WorktreeCreate":[null]}}`} {
		write(t, path, data, 0o600)
		r = Run(context.Background(), Options{StartDir: root, Fix: true})
		if f := finding(r, "claude.hooks", path); f == nil || f.Severity != "fail" || f.Repairable {
			t.Fatalf("malformed hooks accepted: %s %+v", data, f)
		}
	}
	write(t, path, `{"hooks":{"WorktreeCreate":[{"hooks":[{"type":"command","command":"/definitely-missing/wt claude hook-worktree-create"}]}]}}`, 0o600)
	r = Run(context.Background(), Options{StartDir: root, Fix: true})
	if f := finding(r, "claude.hooks.manual", path); f == nil || f.Repairable {
		t.Fatalf("missing executable should require manual repair: %+v", r)
	}
	write(t, path, `{"permissions":{"allow":[]}}`, 0o600)
	before = files(t, root)
	Run(context.Background(), Options{StartDir: root, Fix: true})
	if !reflect.DeepEqual(before, files(t, root)) {
		t.Fatal("added absent hooks")
	}
}
