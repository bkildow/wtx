package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureGitExclude_CreatesInfoDir(t *testing.T) {
	gitDir := t.TempDir()

	if err := EnsureGitExclude(gitDir, false); err != nil {
		t.Fatalf("EnsureGitExclude: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(gitDir, "info", "exclude"))
	if err != nil {
		t.Fatalf("reading exclude: %v", err)
	}

	content := string(data)
	for _, p := range excludePatterns {
		if !strings.Contains(content, p) {
			t.Errorf("exclude file missing pattern %q", p)
		}
	}
}

func TestEnsureGitExclude_AppendsToExisting(t *testing.T) {
	gitDir := t.TempDir()
	infoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	original := "# git default excludes\n*.swp\n"
	if err := os.WriteFile(filepath.Join(infoDir, "exclude"), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureGitExclude(gitDir, false); err != nil {
		t.Fatalf("EnsureGitExclude: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(infoDir, "exclude"))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.HasPrefix(content, original) {
		t.Error("original content was not preserved")
	}
	for _, p := range excludePatterns {
		if !strings.Contains(content, p) {
			t.Errorf("exclude file missing pattern %q", p)
		}
	}
}

func TestEnsureGitExclude_Idempotent(t *testing.T) {
	gitDir := t.TempDir()

	if err := EnsureGitExclude(gitDir, false); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGitExclude(gitDir, false); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(gitDir, "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	for _, p := range excludePatterns {
		if strings.Count(content, p) != 1 {
			t.Errorf("pattern %q appears %d times, want 1", p, strings.Count(content, p))
		}
	}
}

func TestEnsureGitExclude_PartialExisting(t *testing.T) {
	gitDir := t.TempDir()
	infoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pre-populate with only the first pattern.
	initial := SetupStateFile + "\n"
	if err := os.WriteFile(filepath.Join(infoDir, "exclude"), []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureGitExclude(gitDir, false); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(infoDir, "exclude"))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if strings.Count(content, SetupStateFile) != 1 {
		t.Errorf("SetupStateFile appears %d times, want 1", strings.Count(content, SetupStateFile))
	}
	if !strings.Contains(content, SetupLogFile) {
		t.Error("missing SetupLogFile pattern")
	}
}

func TestEnsureGitExclude_DryRun(t *testing.T) {
	gitDir := t.TempDir()

	if err := EnsureGitExclude(gitDir, true); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(gitDir, "info")); !os.IsNotExist(err) {
		t.Error("dry run should not create info directory")
	}
}

func TestEnsureGitExcludeMigratesLegacy(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "info", "exclude")
	original := "# user rules\n*.swp\n# wt-cli managed files\n.wt-setup.json\n.wt-setup.log\n# another rule\nprivate/"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureGitExclude(dir, true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != original {
		t.Fatalf("dry run changed excludes: %q, %v", data, err)
	}
	for range 2 {
		if err := EnsureGitExclude(dir, false); err != nil {
			t.Fatal(err)
		}
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, pattern := range excludePatterns {
		if strings.Count(content, pattern+"\n") != 1 {
			t.Errorf("missing or duplicate %q: %s", pattern, content)
		}
	}
	if strings.Contains(content, "# wt-cli managed files") || strings.Count(content, "# wtx managed files") != 1 {
		t.Errorf("bad marker migration: %s", content)
	}
	if !strings.Contains(content, "# user rules\n*.swp\n") || !strings.Contains(content, "# another rule\nprivate/\n") {
		t.Errorf("lost user rules: %s", content)
	}
}
