// Package config handles reading and writing .worktree.yml configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bkildow/wtx/internal/disk"
	"gopkg.in/yaml.v3"
)

const (
	ConfigFileName     = ".worktree.yml"
	DefaultGitDir      = ".bare"
	DefaultWorktreeDir = "worktrees"
	DefaultSharedDir   = "shared"
	DefaultMainBranch  = "main"
)

var (
	ErrConfigNotFound = errors.New("config file not found")
	ErrInvalidConfig  = errors.New("invalid config")
)

type Config struct {
	Version          int      `yaml:"version"`
	GitDir           string   `yaml:"git_dir"`
	WorktreeDir      string   `yaml:"worktree_dir"`
	SharedDir        string   `yaml:"shared_dir"`
	MainBranch       string   `yaml:"main_branch,omitempty"`
	Setup            []string `yaml:"setup,omitempty"`
	ParallelSetup    []string `yaml:"parallel_setup,omitempty"`
	Teardown         []string `yaml:"teardown,omitempty"`
	ParallelTeardown []string `yaml:"parallel_teardown,omitempty"`
	BackgroundSetup  bool     `yaml:"background_setup,omitempty"`
	Editor           string   `yaml:"editor,omitempty"`

	// Scripts maps a name to an executable path (relative to the project
	// root, or absolute) run via `wtx run <name>`.
	Scripts map[string]string `yaml:"scripts,omitempty"`

	// DiskWarn gates the low-disk-space warning. It is a pointer because the
	// warning defaults to on, so the zero value cannot mean "disabled".
	DiskWarn        *bool `yaml:"disk_warn,omitempty"`
	DiskWarnPercent int   `yaml:"disk_warn_percent,omitempty"`
	DiskWarnGB      int   `yaml:"disk_warn_gb,omitempty"`
}

// DiskThreshold returns the configured low-disk thresholds, or nil when disk
// warnings are disabled. Unset fields fall back to defaults; a negative value
// disables that individual bound.
func (c *Config) DiskThreshold() *disk.Threshold {
	if c.DiskWarn != nil && !*c.DiskWarn {
		return nil
	}

	t := disk.DefaultThreshold()
	switch {
	case c.DiskWarnPercent < 0:
		t.Percent = 0
	case c.DiskWarnPercent > 0:
		t.Percent = float64(c.DiskWarnPercent)
	}
	switch {
	case c.DiskWarnGB < 0:
		t.Bytes = 0
	case c.DiskWarnGB > 0:
		t.Bytes = uint64(c.DiskWarnGB) * disk.BytesPerGB
	}

	return &t
}

// MainBranchOrDefault returns the configured main branch, falling back to DefaultMainBranch.
func (c *Config) MainBranchOrDefault() string {
	if c.MainBranch != "" {
		return c.MainBranch
	}
	return DefaultMainBranch
}

func DefaultConfig() Config {
	return Config{
		Version:     1,
		GitDir:      DefaultGitDir,
		WorktreeDir: DefaultWorktreeDir,
		SharedDir:   DefaultSharedDir,
	}
}

func Load(projectRoot string) (*Config, error) {
	path := filepath.Join(projectRoot, ConfigFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrConfigNotFound
		}
		return nil, err
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, errors.Join(ErrInvalidConfig, err)
	}

	return &cfg, nil
}

func (c *Config) Save(projectRoot string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}

	path := filepath.Join(projectRoot, ConfigFileName)
	return os.WriteFile(path, data, 0o644)
}

func Exists(projectRoot string) bool {
	path := filepath.Join(projectRoot, ConfigFileName)
	_, err := os.Stat(path)
	return err == nil
}

// renderAnnotatedConfig builds YAML with documentation comments.
// If cfg is nil, defaults are used with optional fields commented out.
// If cfg is non-nil, existing values are rendered uncommented.
func renderAnnotatedConfig(cfg *Config) string {
	var b strings.Builder

	b.WriteString("# wtx - worktree project configuration\n")
	b.WriteString("# https://github.com/bkildow/wtx\n\n")

	b.WriteString("# Config schema version (do not change)\n")
	if cfg != nil {
		fmt.Fprintf(&b, "version: %d\n", cfg.Version)
	} else {
		b.WriteString("version: 1\n")
	}

	b.WriteString("\n# Path to the git directory (.bare for cloned projects, .git for initialized)\n")
	if cfg != nil {
		fmt.Fprintf(&b, "git_dir: %s\n", cfg.GitDir)
	} else {
		fmt.Fprintf(&b, "git_dir: %s\n", DefaultGitDir)
	}

	b.WriteString("\n# Directory name for worktrees (relative to project root)\n")
	if cfg != nil {
		fmt.Fprintf(&b, "worktree_dir: %s\n", cfg.WorktreeDir)
	} else {
		fmt.Fprintf(&b, "worktree_dir: %s\n", DefaultWorktreeDir)
	}

	b.WriteString("\n# Directory for shared files (copy/ and symlink/ subdirectories)\n")
	if cfg != nil {
		fmt.Fprintf(&b, "shared_dir: %s\n", cfg.SharedDir)
	} else {
		fmt.Fprintf(&b, "shared_dir: %s\n", DefaultSharedDir)
	}

	b.WriteString("\n# The primary branch of the repository (used as base for new branches, branch ref protected from deletion)\n")
	if cfg != nil && cfg.MainBranch != "" {
		fmt.Fprintf(&b, "main_branch: %s\n", cfg.MainBranch)
	} else {
		b.WriteString("# main_branch: main\n")
	}

	b.WriteString("\n# Editor for 'wtx open' (e.g. cursor, code, zed)\n")
	b.WriteString("# Falls back to $EDITOR, then auto-detects\n")
	if cfg != nil && cfg.Editor != "" {
		fmt.Fprintf(&b, "editor: %s\n", cfg.Editor)
	} else {
		b.WriteString("# editor: cursor\n")
	}

	b.WriteString("\n# Run setup hooks in the background (default: false)\n")
	b.WriteString("# Override per-command with --background or --foreground\n")
	if cfg != nil && cfg.BackgroundSetup {
		b.WriteString("background_setup: true\n")
	} else {
		b.WriteString("# background_setup: false\n")
	}

	b.WriteString("\n# Warn when free disk space runs low and suggest 'wtx prune' (default: on)\n")
	b.WriteString("# Checked on 'wtx add' and 'wtx status'. Set WTX_NO_DISK_WARN=1 to silence per-command.\n")
	if cfg != nil && cfg.DiskWarn != nil && !*cfg.DiskWarn {
		b.WriteString("disk_warn: false\n")
	} else {
		b.WriteString("# disk_warn: false\n")
	}

	b.WriteString("\n# Warn below this percentage of free space (-1 to disable this bound)\n")
	if cfg != nil && cfg.DiskWarnPercent != 0 {
		fmt.Fprintf(&b, "disk_warn_percent: %d\n", cfg.DiskWarnPercent)
	} else {
		fmt.Fprintf(&b, "# disk_warn_percent: %d\n", disk.DefaultWarnPercent)
	}

	b.WriteString("\n# Warn below this many GB of free space (-1 to disable this bound)\n")
	if cfg != nil && cfg.DiskWarnGB != 0 {
		fmt.Fprintf(&b, "disk_warn_gb: %d\n", cfg.DiskWarnGB)
	} else {
		fmt.Fprintf(&b, "# disk_warn_gb: %d\n", disk.DefaultWarnGB)
	}

	b.WriteString("\n# Commands to run after creating a new worktree\n")
	if cfg != nil && len(cfg.Setup) > 0 {
		b.WriteString("setup:\n")
		for _, s := range cfg.Setup {
			fmt.Fprintf(&b, "  - %s\n", yamlQuote(s))
		}
	} else {
		b.WriteString("# setup:\n")
		b.WriteString("#   - npm install\n")
		b.WriteString("#   - cp .env.example .env\n")
	}

	b.WriteString("\n# Commands to run in parallel after creating a new worktree\n")
	b.WriteString("# These run concurrently after serial setup hooks complete\n")
	if cfg != nil && len(cfg.ParallelSetup) > 0 {
		b.WriteString("parallel_setup:\n")
		for _, s := range cfg.ParallelSetup {
			fmt.Fprintf(&b, "  - %s\n", yamlQuote(s))
		}
	} else {
		b.WriteString("# parallel_setup:\n")
		b.WriteString("#   - npm install\n")
		b.WriteString("#   - bundle install\n")
	}

	b.WriteString("\n# Commands to run before removing a worktree\n")
	if cfg != nil && len(cfg.Teardown) > 0 {
		b.WriteString("teardown:\n")
		for _, t := range cfg.Teardown {
			fmt.Fprintf(&b, "  - %s\n", yamlQuote(t))
		}
	} else {
		b.WriteString("# teardown:\n")
		b.WriteString("#   - docker compose down\n")
	}

	b.WriteString("\n# Commands to run in parallel before removing a worktree\n")
	b.WriteString("# These run concurrently after serial teardown hooks complete\n")
	if cfg != nil && len(cfg.ParallelTeardown) > 0 {
		b.WriteString("parallel_teardown:\n")
		for _, t := range cfg.ParallelTeardown {
			fmt.Fprintf(&b, "  - %s\n", yamlQuote(t))
		}
	} else {
		b.WriteString("# parallel_teardown:\n")
		b.WriteString("#   - docker compose down\n")
		b.WriteString("#   - make clean\n")
	}

	b.WriteString("\n# Named scripts run via 'wtx run <name>' from any worktree\n")
	b.WriteString("# Paths are executables resolved relative to the project root\n")
	if cfg != nil && len(cfg.Scripts) > 0 {
		b.WriteString("scripts:\n")
		names := make([]string, 0, len(cfg.Scripts))
		for name := range cfg.Scripts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "  %s: %s\n", yamlQuote(name), yamlQuote(cfg.Scripts[name]))
		}
	} else {
		b.WriteString("# scripts:\n")
		b.WriteString("#   refresh: bin/refresh-snapshot\n")
		b.WriteString("#   seed: bin/seed\n")
	}

	return b.String()
}

// yamlQuote wraps a string in double quotes if it contains characters
// that need quoting in YAML, otherwise returns it bare.
func yamlQuote(s string) string {
	if strings.ContainsAny(s, ":{}[]&*?|>!%#`@,\"'\\$\n") || s == "" {
		return fmt.Sprintf("%q", s)
	}
	return s
}

// WriteAnnotated writes a fresh .worktree.yml with default values and
// documentation comments for every field.
func WriteAnnotated(projectRoot string) error {
	content := renderAnnotatedConfig(nil)
	path := filepath.Join(projectRoot, ConfigFileName)
	return os.WriteFile(path, []byte(content), 0o644)
}

// WriteAnnotatedWithValues writes .worktree.yml using the given config's
// values, with documentation comments for every field.
func WriteAnnotatedWithValues(projectRoot string, cfg *Config) error {
	content := renderAnnotatedConfig(cfg)
	path := filepath.Join(projectRoot, ConfigFileName)
	return os.WriteFile(path, []byte(content), 0o644)
}
