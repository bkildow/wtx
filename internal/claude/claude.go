// Package claude manages Claude Code hook configuration for wt projects.
package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
)

const settingsFile = ".claude/settings.local.json"

// ErrManualMigration marks recognized legacy hooks that cannot be rewritten
// in place safely and need a manual edit.
var ErrManualMigration = errors.New("cannot rewrite legacy hook commands in place; edit them manually")

// MigrateLegacyHooks updates only existing, recognized legacy hook commands.
// unresolved counts commands whose replacement executable is unavailable.
// Commands are replaced in the original bytes so formatting, key order, and
// unrelated content are preserved exactly.
func MigrateLegacyHooks(data []byte) (updated []byte, changed, unresolved int, err error) {
	settings, err := decodeSettings(data)
	if err != nil {
		return nil, 0, 0, err
	}
	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		if settings["hooks"] != nil {
			return nil, 0, 0, fmt.Errorf("hooks must be an object")
		}
		return data, 0, 0, nil
	}
	replacements := map[string]string{}
	for _, event := range []string{HookWorktreeCreate, HookWorktreeRemove} {
		// Absent or null values are tolerated like ConfigureHooks does; other
		// shapes are malformed.
		groups, ok := hooks[event].([]any)
		if !ok && hooks[event] != nil {
			return nil, 0, 0, fmt.Errorf("%s hooks must be an array of objects", event)
		}
		for _, value := range groups {
			group, ok := value.(map[string]any)
			if !ok {
				return nil, 0, 0, fmt.Errorf("%s hooks must be an array of objects", event)
			}
			entries, ok := group["hooks"].([]any)
			if !ok && group["hooks"] != nil {
				return nil, 0, 0, fmt.Errorf("%s group hooks must be an array", event)
			}
			for _, entry := range entries {
				hook, _ := entry.(map[string]any)
				binary, ok := managedBinary(hook, event, "")
				if !ok || filepath.Base(binary) != "wt" {
					continue
				}
				command := hook["command"].(string)
				suffix := " claude " + hookSubcommand(event)
				replacement := "wtx"
				if binary != "wt" {
					if !filepath.IsAbs(binary) {
						unresolved++
						continue
					}
					replacement = filepath.Join(filepath.Dir(binary), "wtx")
				}
				if _, err := exec.LookPath(replacement); err != nil {
					unresolved++
					continue
				}
				hook["command"] = replacement + suffix
				replacements[command] = replacement + suffix
				changed++
			}
		}
	}
	if changed == 0 {
		return data, 0, unresolved, nil
	}
	updated = data
	for from, to := range replacements {
		updated = bytes.ReplaceAll(updated, jsonString(from), jsonString(to))
	}
	// Refuse if the textual replacement touched anything but the hook commands
	// (e.g. the same string elsewhere, or an escaped spelling it missed).
	got, err := decodeSettings(updated)
	if err != nil || !reflect.DeepEqual(got, settings) {
		return nil, 0, 0, ErrManualMigration
	}
	return updated, changed, unresolved, nil
}

func decodeSettings(data []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var settings map[string]any
	if err := decoder.Decode(&settings); err != nil {
		return nil, err
	}
	if settings == nil {
		return nil, fmt.Errorf("settings must be an object")
	}
	return settings, nil
}

func jsonString(s string) []byte {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(s)
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// Hook event names used by Claude Code.
const (
	HookWorktreeCreate = "WorktreeCreate"
	HookWorktreeRemove = "WorktreeRemove"
)

// ConfigureHooks writes WorktreeCreate and WorktreeRemove hooks into
// .claude/settings.local.json, deep-merging with any existing settings.
// wtBinary is the command to invoke (e.g. "wtx" or "/opt/bin/wtx").
func ConfigureHooks(projectRoot, wtBinary string) error {
	path := filepath.Join(projectRoot, settingsFile)

	existing, err := readSettings(path)
	if err != nil {
		return err
	}

	hooks, ok := existing["hooks"].(map[string]any)
	if !ok {
		if existing["hooks"] != nil {
			return fmt.Errorf("hooks must be an object")
		}
		hooks = make(map[string]any)
	}
	for event, generated := range buildHooksConfig(wtBinary) {
		groups, ok := hooks[event].([]any)
		if !ok && hooks[event] != nil {
			return fmt.Errorf("%s hooks must be an array", event)
		}
		found := false
		for _, value := range groups {
			group, _ := value.(map[string]any)
			entries, _ := group["hooks"].([]any)
			for _, entry := range entries {
				hook, _ := entry.(map[string]any)
				if isManagedHook(hook, event, wtBinary) {
					hook["command"] = wtBinary + " claude " + hookSubcommand(event)
					found = true
				}
			}
		}
		if !found {
			groups = append(groups, generated.([]any)...)
		}
		hooks[event] = groups
	}
	existing["hooks"] = hooks

	return writeSettings(path, existing)
}

// IsHooksConfigured checks whether both managed wt/wtx hooks are present.
func IsHooksConfigured(projectRoot string) bool {
	path := filepath.Join(projectRoot, settingsFile)

	settings, err := readSettings(path)
	if err != nil {
		return false
	}

	hooks, ok := settings["hooks"]
	if !ok {
		return false
	}

	hooksMap, ok := hooks.(map[string]any)
	if !ok {
		return false
	}

	for _, event := range []string{HookWorktreeCreate, HookWorktreeRemove} {
		found := false
		groups, _ := hooksMap[event].([]any)
		for _, value := range groups {
			group, _ := value.(map[string]any)
			entries, _ := group["hooks"].([]any)
			for _, entry := range entries {
				hook, _ := entry.(map[string]any)
				if isManagedHook(hook, event, "") {
					found = true
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// RemoveHooks removes recognized wt/wtx worktree hooks from
// .claude/settings.local.json, preserving custom hooks and other settings.
func RemoveHooks(projectRoot string) error {
	path := filepath.Join(projectRoot, settingsFile)

	settings, err := readSettings(path)
	if err != nil {
		return err
	}

	hooks, ok := settings["hooks"]
	if !ok {
		return nil
	}

	hooksMap, ok := hooks.(map[string]any)
	if !ok {
		return nil
	}

	for _, event := range []string{HookWorktreeCreate, HookWorktreeRemove} {
		groups, ok := hooksMap[event].([]any)
		if !ok {
			continue
		}
		keptGroups := make([]any, 0, len(groups))
		for _, value := range groups {
			group, _ := value.(map[string]any)
			entries, ok := group["hooks"].([]any)
			if !ok {
				keptGroups = append(keptGroups, value)
				continue
			}
			kept := make([]any, 0, len(entries))
			removed := false
			for _, entry := range entries {
				hook, _ := entry.(map[string]any)
				if isManagedHook(hook, event, "") {
					removed = true
				} else {
					kept = append(kept, entry)
				}
			}
			if !removed {
				keptGroups = append(keptGroups, value)
			} else if len(kept) > 0 {
				group["hooks"] = kept
				keptGroups = append(keptGroups, group)
			}
		}
		if len(keptGroups) == 0 {
			delete(hooksMap, event)
		} else {
			hooksMap[event] = keptGroups
		}
	}

	if len(hooksMap) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooksMap
	}

	return writeSettings(path, settings)
}

// Recognize only commands generated by this package, not shell expressions
// that happen to contain a managed command.
func isManagedHook(hook map[string]any, event, requestedBinary string) bool {
	_, ok := managedBinary(hook, event, requestedBinary)
	return ok
}

// managedBinary returns the executable of a recognized managed hook command.
func managedBinary(hook map[string]any, event, requestedBinary string) (string, bool) {
	if hook["type"] != "command" {
		return "", false
	}
	command, ok := hook["command"].(string)
	if !ok {
		return "", false
	}
	binary, ok := strings.CutSuffix(command, " claude "+hookSubcommand(event))
	if !ok {
		return "", false
	}
	if requestedBinary != "" && binary == requestedBinary {
		return binary, true
	}
	if strings.ContainsAny(binary, " \t\r\n;|&$`<>\\\"'") {
		return "", false
	}
	name := filepath.Base(binary)
	return binary, name == "wt" || name == "wtx"
}

func hookSubcommand(event string) string {
	if event == HookWorktreeCreate {
		return "hook-worktree-create"
	}
	return "hook-worktree-remove"
}

func buildHooksConfig(wtBinary string) map[string]any {
	return map[string]any{
		HookWorktreeCreate: []any{
			map[string]any{
				"matcher": "",
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": wtBinary + " claude hook-worktree-create",
					},
				},
			},
		},
		HookWorktreeRemove: []any{
			map[string]any{
				"matcher": "",
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": wtBinary + " claude hook-worktree-remove",
					},
				},
			},
		},
	}
}

func readSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]any), nil
		}
		return nil, err
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil, err
	}

	if settings == nil {
		settings = make(map[string]any)
	}
	return settings, nil
}

func writeSettings(path string, settings map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
