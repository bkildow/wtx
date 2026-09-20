package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestConfigureHooks_newFile(t *testing.T) {
	dir := t.TempDir()

	if err := ConfigureHooks(dir, "wtx"); err != nil {
		t.Fatalf("ConfigureHooks: %v", err)
	}

	settings := readSettingsFile(t, dir)

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing or wrong type")
	}

	for _, event := range []string{HookWorktreeCreate, HookWorktreeRemove} {
		arr, ok := hooks[event].([]any)
		if !ok || len(arr) == 0 {
			t.Fatalf("%s missing or empty", event)
		}
		entry := arr[0].(map[string]any)
		hookArr := entry["hooks"].([]any)
		hook := hookArr[0].(map[string]any)
		if hook["type"] != "command" {
			t.Errorf("%s hook type = %v, want command", event, hook["type"])
		}
		cmd := hook["command"].(string)
		if event == HookWorktreeCreate && cmd != "wtx claude hook-worktree-create" {
			t.Errorf("WorktreeCreate command = %q", cmd)
		}
		if event == HookWorktreeRemove && cmd != "wtx claude hook-worktree-remove" {
			t.Errorf("WorktreeRemove command = %q", cmd)
		}
	}
}

func TestConfigureHooks_preservesExistingSettings(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, settingsFile)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write existing settings with other fields.
	existing := map[string]any{
		"permissions": map[string]any{"allow": []any{"Bash"}},
		"customKey":   "customValue",
	}
	data, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigureHooks(dir, "wtx"); err != nil {
		t.Fatalf("ConfigureHooks: %v", err)
	}

	settings := readSettingsFile(t, dir)

	// Verify existing fields preserved.
	if settings["customKey"] != "customValue" {
		t.Errorf("customKey clobbered: %v", settings["customKey"])
	}
	if _, ok := settings["permissions"]; !ok {
		t.Error("permissions key was clobbered")
	}

	// Verify hooks added.
	if _, ok := settings["hooks"]; !ok {
		t.Error("hooks key missing")
	}
}

func TestConfigureHooks_idempotent(t *testing.T) {
	dir := t.TempDir()

	if err := ConfigureHooks(dir, "wtx"); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureHooks(dir, "wtx"); err != nil {
		t.Fatal(err)
	}

	settings := readSettingsFile(t, dir)
	hooks := settings["hooks"].(map[string]any)
	create := hooks["WorktreeCreate"].([]any)
	if len(create) != 1 {
		t.Errorf("expected 1 WorktreeCreate entry, got %d", len(create))
	}
}

func TestConfigureHooks_customBinary(t *testing.T) {
	dir := t.TempDir()

	if err := ConfigureHooks(dir, "/opt/bin/wtx"); err != nil {
		t.Fatal(err)
	}

	settings := readSettingsFile(t, dir)
	hooks := settings["hooks"].(map[string]any)
	create := hooks["WorktreeCreate"].([]any)
	entry := create[0].(map[string]any)
	hookArr := entry["hooks"].([]any)
	hook := hookArr[0].(map[string]any)
	if hook["command"] != "/opt/bin/wtx claude hook-worktree-create" {
		t.Errorf("command = %q", hook["command"])
	}
}

func TestConfigureHooks_createsDirectory(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")

	// Ensure .claude/ doesn't exist yet.
	if _, err := os.Stat(claudeDir); !os.IsNotExist(err) {
		t.Fatal(".claude/ should not exist yet")
	}

	if err := ConfigureHooks(dir, "wtx"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(claudeDir); err != nil {
		t.Fatalf(".claude/ was not created: %v", err)
	}
}

func TestIsHooksConfigured(t *testing.T) {
	dir := t.TempDir()

	if IsHooksConfigured(dir) {
		t.Error("should be false before configuring")
	}

	if err := ConfigureHooks(dir, "wtx"); err != nil {
		t.Fatal(err)
	}

	if !IsHooksConfigured(dir) {
		t.Error("should be true after configuring")
	}
}

func TestRemoveHooks(t *testing.T) {
	dir := t.TempDir()

	if err := ConfigureHooks(dir, "wtx"); err != nil {
		t.Fatal(err)
	}

	if err := RemoveHooks(dir); err != nil {
		t.Fatal(err)
	}

	if IsHooksConfigured(dir) {
		t.Error("hooks should be removed")
	}

	// File should still exist (even if hooks section is gone).
	settings := readSettingsFile(t, dir)
	if _, ok := settings["hooks"]; ok {
		t.Error("hooks key should be removed when empty")
	}
}

func TestRemoveHooks_preservesOtherHooks(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, settingsFile)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}

	// Write settings with wtx hooks + a custom hook.
	settings := map[string]any{
		"hooks": map[string]any{
			HookWorktreeCreate: buildHooksConfig("wtx")[HookWorktreeCreate],
			HookWorktreeRemove: buildHooksConfig("wtx")[HookWorktreeRemove],
			"PreToolUse":       []any{map[string]any{"matcher": "Bash"}},
		},
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveHooks(dir); err != nil {
		t.Fatal(err)
	}

	result := readSettingsFile(t, dir)
	hooks := result["hooks"].(map[string]any)
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("PreToolUse hook was clobbered")
	}
	if _, ok := hooks[HookWorktreeCreate]; ok {
		t.Error("WorktreeCreate should be removed")
	}
}

func readSettingsFile(t *testing.T, projectRoot string) map[string]any {
	t.Helper()
	path := filepath.Join(projectRoot, settingsFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return settings
}

func TestConfigureHooks_migratesAndPreservesCustomHooks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared", "symlink")
	custom := map[string]any{"type": "command", "command": "echo wt claude hook-worktree-create", "timeout": float64(5)}
	managed := map[string]any{"type": "command", "command": "/opt/bin/wt claude hook-worktree-create", "timeout": float64(30), "async": true}
	settings := map[string]any{"permissions": map[string]any{"allow": []any{"Bash"}}, "hooks": map[string]any{
		HookWorktreeCreate: []any{map[string]any{"matcher": "custom", "hooks": []any{managed, custom}}},
		HookWorktreeRemove: buildHooksConfig("wt")[HookWorktreeRemove],
		"PreToolUse":       []any{map[string]any{"matcher": "Bash", "hooks": []any{custom}}},
	}}
	if err := writeSettings(filepath.Join(dir, settingsFile), settings); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := ConfigureHooks(dir, "wtx"); err != nil {
			t.Fatal(err)
		}
	}
	managed["command"] = "wtx claude hook-worktree-create"
	settings["hooks"].(map[string]any)[HookWorktreeRemove] = buildHooksConfig("wtx")[HookWorktreeRemove]
	if got := readSettingsFile(t, dir); !reflect.DeepEqual(got, settings) {
		t.Fatalf("migration changed custom settings: got %#v, want %#v", got, settings)
	}
	if err := RemoveHooks(dir); err != nil {
		t.Fatal(err)
	}
	got := readSettingsFile(t, dir)["hooks"].(map[string]any)
	groups := got[HookWorktreeCreate].([]any)
	entries := groups[0].(map[string]any)["hooks"].([]any)
	if len(entries) != 1 || !reflect.DeepEqual(entries[0], custom) {
		t.Fatalf("custom hook removed: %#v", groups)
	}
	if _, ok := got["PreToolUse"]; !ok {
		t.Fatal("custom event removed")
	}
	if IsHooksConfigured(dir) {
		t.Fatal("custom hooks counted as managed")
	}
}

func TestConfigureHooks_preservesMalformedSettings(t *testing.T) {
	for _, data := range []string{`{"hooks":[]}`, `{"hooks":{"WorktreeCreate":{}}}`, `{broken`} {
		t.Run(data, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, settingsFile)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ConfigureHooks(dir, "wtx"); err == nil {
				t.Fatal("expected invalid settings error")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != data {
				t.Fatal("invalid settings overwritten")
			}
		})
	}
}

func TestManagedHookRecognition(t *testing.T) {
	for _, command := range []string{"wt claude hook-worktree-create", "wtx claude hook-worktree-create", "/opt/bin/wt claude hook-worktree-create"} {
		if !isManagedHook(map[string]any{"type": "command", "command": command}, HookWorktreeCreate, "") {
			t.Errorf("missed managed command %q", command)
		}
	}
	for _, command := range []string{"echo wt claude hook-worktree-create", "wt claude hook-worktree-create && echo done", "other claude hook-worktree-create", "$(echo wt) claude hook-worktree-create", "wt claude hook-worktree-remove"} {
		if isManagedHook(map[string]any{"type": "command", "command": command}, HookWorktreeCreate, "") {
			t.Errorf("claimed custom command %q", command)
		}
	}
}

func TestConfigureHooks_customBinaryMigration(t *testing.T) {
	dir := t.TempDir()
	if err := ConfigureHooks(dir, "wt"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := ConfigureHooks(dir, "/opt/bin/custom"); err != nil {
			t.Fatal(err)
		}
	}
	expected := map[string]any{"hooks": buildHooksConfig("/opt/bin/custom")}
	if got := readSettingsFile(t, dir); !reflect.DeepEqual(got, expected) {
		t.Fatalf("got %#v, want %#v", got, expected)
	}
}
