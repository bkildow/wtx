package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/bkildow/wtx/internal/ui"
)

// excludePatterns are the file patterns managed by wtx that should be
// added to the repository's info/exclude file.
var excludePatterns = []string{
	SetupStateFile,
	SetupLogFile,
	legacySetupStateFile,
	legacySetupLogFile,
}

// EnsureGitExclude ensures that wtx-managed file patterns are listed in
// the repository's info/exclude file so they don't appear as untracked.
func EnsureGitExclude(gitDir string, dryRun bool) error {
	infoDir := filepath.Join(gitDir, "info")
	excludePath := filepath.Join(infoDir, "exclude")

	// Read existing content.
	existing, err := os.ReadFile(excludePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	updated := ManagedExclude(existing)
	if string(updated) == string(existing) {
		return nil
	}
	if dryRun {
		ui.DryRunNotice("update managed exclusions in " + excludePath)
		return nil
	}
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(excludePath, updated, 0o600)
}

// ManagedExclude returns the updated exclusions, preserving custom content.
func ManagedExclude(existing []byte) []byte {
	lines := strings.Split(string(existing), "\n")
	present := make(map[string]bool)
	migrated := false
	for i, line := range lines {
		if strings.TrimSpace(line) == "# wt-cli managed files" {
			lines[i] = "# wtx managed files"
			migrated = true
		}
		present[strings.TrimSpace(lines[i])] = true
	}

	var missing []string
	for _, p := range excludePatterns {
		if !present[p] {
			missing = append(missing, p)
		}
	}

	if len(missing) == 0 && !migrated {
		return existing
	}

	// Preserve all existing patterns and comments while migrating the marker.
	var buf strings.Builder
	buf.WriteString(strings.Join(lines, "\n"))

	// Ensure we start on a new line if the file has content without a trailing newline.
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		buf.WriteByte('\n')
	}

	if !present["# wtx managed files"] {
		buf.WriteString("# wtx managed files\n")
	}
	for _, p := range missing {
		buf.WriteString(p)
		buf.WriteByte('\n')
	}

	return []byte(buf.String())
}
