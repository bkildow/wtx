package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkildow/wtx/internal/disk"
)

func TestLoadValidConfig(t *testing.T) {
	dir := t.TempDir()
	content := `version: 1
git_dir: .bare
worktree_dir: trees
setup:
  - npm install
parallel_setup:
  - bundle install
  - pip install -r requirements.txt
teardown:
  - "docker compose down"
parallel_teardown:
  - make clean
editor: cursor
scripts:
  refresh: bin/refresh-snapshot
  seed: /opt/tools/seed
`
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Scripts) != 2 || cfg.Scripts["refresh"] != "bin/refresh-snapshot" || cfg.Scripts["seed"] != "/opt/tools/seed" {
		t.Errorf("scripts = %v, want refresh and seed entries", cfg.Scripts)
	}

	if cfg.Version != 1 {
		t.Errorf("version = %d, want 1", cfg.Version)
	}
	if cfg.GitDir != ".bare" {
		t.Errorf("git_dir = %q, want %q", cfg.GitDir, ".bare")
	}
	if cfg.WorktreeDir != "trees" {
		t.Errorf("worktree_dir = %q, want %q", cfg.WorktreeDir, "trees")
	}
	if len(cfg.Setup) != 1 || cfg.Setup[0] != "npm install" {
		t.Errorf("setup = %v, want [npm install]", cfg.Setup)
	}
	if len(cfg.ParallelSetup) != 2 || cfg.ParallelSetup[0] != "bundle install" {
		t.Errorf("parallel_setup = %v, want [bundle install, pip install -r requirements.txt]", cfg.ParallelSetup)
	}
	if len(cfg.Teardown) != 1 || cfg.Teardown[0] != "docker compose down" {
		t.Errorf("teardown = %v, want [docker compose down]", cfg.Teardown)
	}
	if len(cfg.ParallelTeardown) != 1 || cfg.ParallelTeardown[0] != "make clean" {
		t.Errorf("parallel_teardown = %v, want [make clean]", cfg.ParallelTeardown)
	}
	if cfg.Editor != "cursor" {
		t.Errorf("editor = %q, want %q", cfg.Editor, "cursor")
	}
}

func TestLoadMinimalConfig(t *testing.T) {
	dir := t.TempDir()
	content := `version: 2
`
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Version != 2 {
		t.Errorf("version = %d, want 2", cfg.Version)
	}
	// Default should be applied for git_dir
	if cfg.GitDir != DefaultGitDir {
		t.Errorf("git_dir = %q, want default %q", cfg.GitDir, DefaultGitDir)
	}
	if cfg.WorktreeDir != DefaultWorktreeDir {
		t.Errorf("worktree_dir = %q, want default %q", cfg.WorktreeDir, DefaultWorktreeDir)
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir)
	if !errors.Is(err, ErrConfigNotFound) {
		t.Errorf("err = %v, want ErrConfigNotFound", err)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	content := `{{{not yaml`
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	original := &Config{
		Version:     1,
		GitDir:      ".bare",
		WorktreeDir: "trees",
		Setup:       []string{"make build", "make test"},
		Teardown:    []string{"make clean"},
		Editor:      "nvim",
	}

	if err := original.Save(dir); err != nil {
		t.Fatalf("save error: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	if loaded.Version != original.Version {
		t.Errorf("version = %d, want %d", loaded.Version, original.Version)
	}
	if loaded.GitDir != original.GitDir {
		t.Errorf("git_dir = %q, want %q", loaded.GitDir, original.GitDir)
	}
	if loaded.WorktreeDir != original.WorktreeDir {
		t.Errorf("worktree_dir = %q, want %q", loaded.WorktreeDir, original.WorktreeDir)
	}
	if len(loaded.Setup) != len(original.Setup) {
		t.Errorf("setup len = %d, want %d", len(loaded.Setup), len(original.Setup))
	}
	if len(loaded.Teardown) != len(original.Teardown) {
		t.Errorf("teardown len = %d, want %d", len(loaded.Teardown), len(original.Teardown))
	}
	if loaded.Teardown[0] != "make clean" {
		t.Errorf("teardown[0] = %q, want %q", loaded.Teardown[0], "make clean")
	}
	if loaded.Editor != original.Editor {
		t.Errorf("editor = %q, want %q", loaded.Editor, original.Editor)
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()

	if Exists(dir) {
		t.Error("Exists should return false when config file is missing")
	}

	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if !Exists(dir) {
		t.Error("Exists should return true when config file is present")
	}
}

func TestWriteAnnotated(t *testing.T) {
	dir := t.TempDir()

	if err := WriteAnnotated(dir); err != nil {
		t.Fatalf("WriteAnnotated error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	content := string(data)

	// Should contain documentation comments
	if !strings.Contains(content, "# wt - worktree project configuration") {
		t.Error("missing header comment")
	}

	// Should have defaults
	if !strings.Contains(content, "version: 1") {
		t.Error("missing version default")
	}
	if !strings.Contains(content, "git_dir: .bare") {
		t.Error("missing git_dir default")
	}
	if !strings.Contains(content, "worktree_dir: worktrees") {
		t.Error("missing worktree_dir default")
	}

	// main_branch should be commented out
	if !strings.Contains(content, "# main_branch: main") {
		t.Error("main_branch should be commented out as example")
	}

	// Optional fields should be commented out
	if !strings.Contains(content, "# editor: cursor") {
		t.Error("editor should be commented out as example")
	}
	if !strings.Contains(content, "# setup:") {
		t.Error("setup should be commented out as example")
	}
	if !strings.Contains(content, "# parallel_setup:") {
		t.Error("parallel_setup should be commented out as example")
	}
	if !strings.Contains(content, "# teardown:") {
		t.Error("teardown should be commented out as example")
	}
	if !strings.Contains(content, "# parallel_teardown:") {
		t.Error("parallel_teardown should be commented out as example")
	}
	if !strings.Contains(content, "# disk_warn: false") {
		t.Error("disk_warn should be commented out as example")
	}
	if !strings.Contains(content, "# disk_warn_percent: 10") {
		t.Error("disk_warn_percent should be commented out with its default")
	}
	if !strings.Contains(content, "# disk_warn_gb: 10") {
		t.Error("disk_warn_gb should be commented out with its default")
	}

	// Should be loadable
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("annotated config should be loadable: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("version = %d, want 1", cfg.Version)
	}
	if cfg.GitDir != DefaultGitDir {
		t.Errorf("git_dir = %q, want %q", cfg.GitDir, DefaultGitDir)
	}
	if cfg.WorktreeDir != DefaultWorktreeDir {
		t.Errorf("worktree_dir = %q, want %q", cfg.WorktreeDir, DefaultWorktreeDir)
	}
}

func TestWriteAnnotatedWithValues(t *testing.T) {
	dir := t.TempDir()

	existing := &Config{
		Version:     1,
		GitDir:      ".bare",
		WorktreeDir: "trees",
		MainBranch:  "develop",
		Setup:       []string{"npm install", "cp .env.example .env"},
		Teardown:    []string{"docker compose down"},
		Editor:      "cursor",
		Scripts:     map[string]string{"seed": "bin/seed", "refresh": "bin/refresh:snapshot", "db:reset": "bin/db-reset"},
	}

	if err := WriteAnnotatedWithValues(dir, existing); err != nil {
		t.Fatalf("WriteAnnotatedWithValues error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	content := string(data)

	// Should contain documentation comments
	if !strings.Contains(content, "# wt - worktree project configuration") {
		t.Error("missing header comment")
	}

	// main_branch should be uncommented with existing value
	if !strings.Contains(content, "main_branch: develop") {
		t.Error("missing main_branch value")
	}
	if strings.Contains(content, "# main_branch:") {
		t.Error("main_branch should not be commented out when value exists")
	}

	// Editor should be uncommented with existing value
	if !strings.Contains(content, "editor: cursor") {
		t.Error("missing editor value")
	}
	if strings.Contains(content, "# editor: cursor") {
		t.Error("editor should not be commented out when value exists")
	}

	// Setup should be uncommented with existing values
	if !strings.Contains(content, "setup:") {
		t.Error("missing setup section")
	}

	// worktree_dir should be rendered with custom value
	if !strings.Contains(content, "worktree_dir: trees") {
		t.Error("missing worktree_dir custom value")
	}

	// Scripts should be uncommented, sorted by name, with values quoted as needed
	if !strings.Contains(content, "scripts:\n  \"db:reset\": bin/db-reset\n  refresh: \"bin/refresh:snapshot\"\n  seed: bin/seed\n") {
		t.Errorf("scripts block not rendered as expected:\n%s", content)
	}
	if strings.Contains(content, "# scripts:") {
		t.Error("scripts should not be commented out when values exist")
	}

	// Should be loadable and round-trip correctly
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("annotated config with values should be loadable: %v", err)
	}
	if cfg.WorktreeDir != "trees" {
		t.Errorf("worktree_dir = %q, want %q", cfg.WorktreeDir, "trees")
	}
	if cfg.Editor != "cursor" {
		t.Errorf("editor = %q, want %q", cfg.Editor, "cursor")
	}
	if len(cfg.Setup) != 2 {
		t.Errorf("setup len = %d, want 2", len(cfg.Setup))
	}
	if cfg.Setup[0] != "npm install" {
		t.Errorf("setup[0] = %q, want %q", cfg.Setup[0], "npm install")
	}
	if len(cfg.Teardown) != 1 || cfg.Teardown[0] != "docker compose down" {
		t.Errorf("teardown = %v, want [docker compose down]", cfg.Teardown)
	}
	if cfg.Scripts["db:reset"] != "bin/db-reset" || cfg.Scripts["refresh"] != "bin/refresh:snapshot" {
		t.Errorf("scripts did not round-trip: %v", cfg.Scripts)
	}
}

func TestLoadConfigWithGitDirDotGit(t *testing.T) {
	dir := t.TempDir()
	content := `version: 1
git_dir: .git
worktree_dir: worktrees
`
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.GitDir != ".git" {
		t.Errorf("git_dir = %q, want %q", cfg.GitDir, ".git")
	}
	if cfg.WorktreeDir != "worktrees" {
		t.Errorf("worktree_dir = %q, want %q", cfg.WorktreeDir, "worktrees")
	}
}

func TestWriteAnnotatedWithGitDirDotGit(t *testing.T) {
	dir := t.TempDir()

	cfg := &Config{
		Version:     1,
		GitDir:      ".git",
		WorktreeDir: "worktrees",
	}

	if err := WriteAnnotatedWithValues(dir, cfg); err != nil {
		t.Fatalf("WriteAnnotatedWithValues error: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}

	if loaded.GitDir != ".git" {
		t.Errorf("git_dir = %q, want %q", loaded.GitDir, ".git")
	}
}

func TestMainBranchOrDefault(t *testing.T) {
	t.Run("empty returns default", func(t *testing.T) {
		cfg := Config{}
		if got := cfg.MainBranchOrDefault(); got != DefaultMainBranch {
			t.Errorf("MainBranchOrDefault() = %q, want %q", got, DefaultMainBranch)
		}
	})
	t.Run("set value is returned", func(t *testing.T) {
		cfg := Config{MainBranch: "develop"}
		if got := cfg.MainBranchOrDefault(); got != "develop" {
			t.Errorf("MainBranchOrDefault() = %q, want %q", got, "develop")
		}
	})
}

func TestLoadConfigWithMainBranch(t *testing.T) {
	dir := t.TempDir()
	content := `version: 1
git_dir: .bare
main_branch: develop
`
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MainBranch != "develop" {
		t.Errorf("main_branch = %q, want %q", cfg.MainBranch, "develop")
	}
	if cfg.MainBranchOrDefault() != "develop" {
		t.Errorf("MainBranchOrDefault() = %q, want %q", cfg.MainBranchOrDefault(), "develop")
	}
}

func TestLoadConfigWithoutMainBranch(t *testing.T) {
	dir := t.TempDir()
	content := `version: 1
git_dir: .bare
`
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MainBranch != "" {
		t.Errorf("main_branch = %q, want empty (backward compat)", cfg.MainBranch)
	}
	if cfg.MainBranchOrDefault() != DefaultMainBranch {
		t.Errorf("MainBranchOrDefault() = %q, want %q", cfg.MainBranchOrDefault(), DefaultMainBranch)
	}
}

func TestSaveAndLoadMainBranchRoundTrip(t *testing.T) {
	dir := t.TempDir()
	original := &Config{
		Version:    1,
		GitDir:     ".bare",
		MainBranch: "develop",
	}
	if err := original.Save(dir); err != nil {
		t.Fatalf("save error: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if loaded.MainBranch != "develop" {
		t.Errorf("main_branch = %q, want %q", loaded.MainBranch, "develop")
	}
}

func TestYamlQuote(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"npm install", "npm install"},
		{"simple", "simple"},
		{"has: colon", `"has: colon"`},
		{"has $var", `"has $var"`},
		{"", `""`},
	}
	for _, tt := range tests {
		got := yamlQuote(tt.input)
		if got != tt.want {
			t.Errorf("yamlQuote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDiskThresholdDefaults(t *testing.T) {
	cfg := DefaultConfig()

	got := cfg.DiskThreshold()
	if got == nil {
		t.Fatal("DiskThreshold() = nil, want defaults when disk_warn is unset")
	}
	if got.Percent != disk.DefaultWarnPercent {
		t.Errorf("Percent = %v, want %v", got.Percent, float64(disk.DefaultWarnPercent))
	}
	if got.Bytes != disk.DefaultWarnGB*disk.BytesPerGB {
		t.Errorf("Bytes = %v, want %v", got.Bytes, disk.DefaultWarnGB*disk.BytesPerGB)
	}
}

func TestDiskThresholdOverrides(t *testing.T) {
	enabled, disabled := true, false

	tests := []struct {
		name        string
		cfg         Config
		wantNil     bool
		wantPercent float64
		wantBytes   uint64
	}{
		{
			name:    "disk_warn false disables warnings",
			cfg:     Config{DiskWarn: &disabled},
			wantNil: true,
		},
		{
			name:        "disk_warn true keeps defaults",
			cfg:         Config{DiskWarn: &enabled},
			wantPercent: disk.DefaultWarnPercent,
			wantBytes:   disk.DefaultWarnGB * disk.BytesPerGB,
		},
		{
			name:        "custom bounds",
			cfg:         Config{DiskWarnPercent: 25, DiskWarnGB: 50},
			wantPercent: 25,
			wantBytes:   50 * disk.BytesPerGB,
		},
		{
			name:        "negative percent disables only that bound",
			cfg:         Config{DiskWarnPercent: -1, DiskWarnGB: 5},
			wantPercent: 0,
			wantBytes:   5 * disk.BytesPerGB,
		},
		{
			name:        "negative gb disables only that bound",
			cfg:         Config{DiskWarnPercent: 5, DiskWarnGB: -1},
			wantPercent: 5,
			wantBytes:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			got := cfg.DiskThreshold()

			if tt.wantNil {
				if got != nil {
					t.Fatalf("DiskThreshold() = %+v, want nil", *got)
				}
				return
			}
			if got == nil {
				t.Fatal("DiskThreshold() = nil, want a threshold")
			}
			if got.Percent != tt.wantPercent {
				t.Errorf("Percent = %v, want %v", got.Percent, tt.wantPercent)
			}
			if got.Bytes != tt.wantBytes {
				t.Errorf("Bytes = %v, want %v", got.Bytes, tt.wantBytes)
			}
		})
	}
}

func TestLoadConfigWithDiskSettings(t *testing.T) {
	dir := t.TempDir()
	content := `version: 1
disk_warn: false
disk_warn_percent: 20
disk_warn_gb: 40
`
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.DiskWarn == nil || *cfg.DiskWarn {
		t.Errorf("disk_warn = %v, want false", cfg.DiskWarn)
	}
	if cfg.DiskWarnPercent != 20 {
		t.Errorf("disk_warn_percent = %d, want 20", cfg.DiskWarnPercent)
	}
	if cfg.DiskWarnGB != 40 {
		t.Errorf("disk_warn_gb = %d, want 40", cfg.DiskWarnGB)
	}
	if cfg.DiskThreshold() != nil {
		t.Error("DiskThreshold() should be nil when disk_warn is false")
	}
}

func TestWriteAnnotatedWithDiskValues(t *testing.T) {
	dir := t.TempDir()
	disabled := false

	existing := DefaultConfig()
	existing.DiskWarn = &disabled
	existing.DiskWarnPercent = 25
	existing.DiskWarnGB = 30

	if err := WriteAnnotatedWithValues(dir, &existing); err != nil {
		t.Fatalf("WriteAnnotatedWithValues error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ConfigFileName))
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	content := string(data)

	for _, want := range []string{"disk_warn: false", "disk_warn_percent: 25", "disk_warn_gb: 30"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q in annotated config", want)
		}
		if strings.Contains(content, "# "+want) {
			t.Errorf("%q should not be commented out when a value exists", want)
		}
	}

	// Values must survive the round trip.
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("annotated config should be loadable: %v", err)
	}
	if reloaded.DiskWarn == nil || *reloaded.DiskWarn {
		t.Errorf("disk_warn = %v, want false", reloaded.DiskWarn)
	}
	if reloaded.DiskWarnPercent != 25 || reloaded.DiskWarnGB != 30 {
		t.Errorf("thresholds = %d%%/%dGB, want 25%%/30GB", reloaded.DiskWarnPercent, reloaded.DiskWarnGB)
	}
}
