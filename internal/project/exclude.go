package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/bkildow/wtx/internal/ui"
)

// excludePatterns are the file patterns managed by wt-cli that should be
// added to the repository's info/exclude file.
var excludePatterns = []string{
	SetupStateFile,
	SetupLogFile,
}

// EnsureGitExclude ensures that wt-managed file patterns are listed in
// the repository's info/exclude file so they don't appear as untracked.
func EnsureGitExclude(gitDir string, dryRun bool) error {
	infoDir := filepath.Join(gitDir, "info")
	excludePath := filepath.Join(infoDir, "exclude")

	// Read existing content.
	existing, err := os.ReadFile(excludePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	lines := strings.Split(string(existing), "\n")
	present := make(map[string]bool)
	for _, line := range lines {
		present[strings.TrimSpace(line)] = true
	}

	var missing []string
	for _, p := range excludePatterns {
		if !present[p] {
			missing = append(missing, p)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	if dryRun {
		ui.DryRunNotice("append to " + excludePath + ": " + strings.Join(missing, ", "))
		return nil
	}

	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return err
	}

	// Build the block to append.
	var buf strings.Builder

	// Ensure we start on a new line if the file has content without a trailing newline.
	if len(existing) > 0 && existing[len(existing)-1] != '\n' {
		buf.WriteByte('\n')
	}

	if !strings.Contains(string(existing), "# wt-cli managed files") {
		buf.WriteString("# wt-cli managed files\n")
	}
	for _, p := range missing {
		buf.WriteString(p)
		buf.WriteByte('\n')
	}

	f, err := os.OpenFile(excludePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	_, err = f.WriteString(buf.String())
	return err
}
