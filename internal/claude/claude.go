// Package claude manages Claude Code hook configuration for wtx projects.
package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SettingsFile is where the hooks live, relative to the directory
// ConfigureHooks writes into.
const SettingsFile = ".claude/settings.local.json"

// Hook event names used by Claude Code.
const (
	HookWorktreeCreate = "WorktreeCreate"
	HookWorktreeRemove = "WorktreeRemove"
)

// ConfigureHooks writes WorktreeCreate and WorktreeRemove hooks into
// .claude/settings.local.json, deep-merging with any existing settings.
// binary is the command to invoke (e.g. "wtx" or "/opt/bin/wtx").
func ConfigureHooks(projectRoot, binary string) error {
	path := filepath.Join(projectRoot, SettingsFile)

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
	for event, generated := range buildHooksConfig(binary) {
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
				if isManagedHook(hook, event, binary) {
					hook["command"] = binary + " claude " + hookSubcommand(event)
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

// IsHooksConfigured checks whether both managed wtx hooks are present.
func IsHooksConfigured(projectRoot string) bool {
	path := filepath.Join(projectRoot, SettingsFile)

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

// RemoveHooks removes recognized wtx worktree hooks from
// .claude/settings.local.json, preserving custom hooks and other settings.
func RemoveHooks(projectRoot string) error {
	path := filepath.Join(projectRoot, SettingsFile)

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

// isManagedHook reports whether hook runs a wtx hook command this package
// generates: requestedBinary, or a plain path to a wtx executable. Shell
// expressions that happen to contain a managed command are not recognized.
func isManagedHook(hook map[string]any, event, requestedBinary string) bool {
	if hook["type"] != "command" {
		return false
	}
	command, ok := hook["command"].(string)
	if !ok {
		return false
	}
	binary, ok := strings.CutSuffix(command, " claude "+hookSubcommand(event))
	if !ok {
		return false
	}
	if requestedBinary != "" && binary == requestedBinary {
		return true
	}
	if strings.ContainsAny(binary, " \t\r\n;|&$`<>\\\"'") {
		return false
	}
	return filepath.Base(binary) == "wtx"
}

func hookSubcommand(event string) string {
	if event == HookWorktreeCreate {
		return "hook-worktree-create"
	}
	return "hook-worktree-remove"
}

func buildHooksConfig(binary string) map[string]any {
	return map[string]any{
		HookWorktreeCreate: []any{
			map[string]any{
				"matcher": "",
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": binary + " claude hook-worktree-create",
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
						"command": binary + " claude hook-worktree-remove",
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
