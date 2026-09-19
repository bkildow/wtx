package project

import (
	"embed"
	"errors"
	"os"
	"path/filepath"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/ui"
)

//go:embed templates/refresh.sh
var starterFS embed.FS

// StarterScriptName is the script created in the bin directory of new projects.
const StarterScriptName = "refresh"

// StarterScripts returns the default scripts map for a new project, with
// paths relative to projectRoot.
func StarterScripts(projectRoot string, cfg *config.Config) map[string]string {
	rel, err := filepath.Rel(projectRoot, filepath.Join(BinPath(projectRoot, cfg), StarterScriptName))
	if err != nil {
		rel = filepath.Join("bin", StarterScriptName)
	}
	return map[string]string{StarterScriptName: filepath.ToSlash(rel)}
}

// WriteStarterScripts creates the starter refresh script in the bin
// directory. An existing file is never overwritten.
func WriteStarterScripts(projectRoot string, cfg *config.Config, dryRun bool) error {
	dest := filepath.Join(BinPath(projectRoot, cfg), StarterScriptName)

	if dryRun {
		ui.DryRunNotice("write " + dest)
		return nil
	}

	if _, err := os.Stat(dest); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	content, err := starterFS.ReadFile("templates/refresh.sh")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dest, content, 0o755)
}
