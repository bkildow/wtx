package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkildow/wtx/internal/config"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage project configuration",
	}
	cmd.AddCommand(newConfigInitCmd())
	return cmd
}

func newConfigInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Regenerate .worktree.yml as an annotated template, keeping its layout",
		Long: `Regenerate .worktree.yml as an annotated template with documentation comments.

By default the existing file is backed up to .worktree.yml.bak and every
setting except git_dir, worktree_dir, shared_dir and main_branch is reset to
the template, so the project keeps its layout. With --update every existing value is kept.`,
		Args: cobra.NoArgs,
		RunE: runConfigInit,
	}
	cmd.Flags().Bool("update", false, "Merge existing values into the annotated template")
	return cmd
}

func runConfigInit(cmd *cobra.Command, args []string) error {
	dry := IsDryRun()
	update, _ := cmd.Flags().GetBool("update")

	projectRoot, err := findProjectRoot() // raw-load: config init writes .worktree.yml, so it may not hold a valid config yet
	if err != nil {
		return err
	}

	// findProjectRoot only finds a root holding .worktree.yml, so the file
	// exists; it is read once for both the parse and the backup.
	configPath := filepath.Join(projectRoot, config.ConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read existing config: %w", err)
	}
	existing, err := config.Parse(data) // raw-load: config init writes .worktree.yml, so it reads it before a Clone can resolve
	if err != nil {
		return fmt.Errorf("failed to load existing config: %w", err)
	}

	cfg := existing
	step := "Updating " + config.ConfigFileName + " with documentation comments"
	done := "Updated " + config.ConfigFileName + " with documentation comments"
	if !update {
		backupPath := configPath + ".bak"
		ui.Step("Backing up existing config to " + config.ConfigFileName + ".bak")
		if dry {
			ui.DryRunNotice("cp " + configPath + " " + backupPath)
		} else if err := os.WriteFile(backupPath, data, 0o644); err != nil {
			return fmt.Errorf("failed to write backup: %w", err)
		}
		cfg = existing.LayoutOnly()
		step = "Writing fresh " + config.ConfigFileName + " (keeping git_dir, worktree_dir, shared_dir and main_branch)"
		done = "Reset " + config.ConfigFileName + " to the template; the original is in " + config.ConfigFileName + ".bak"
	}

	ui.Step(step)
	if dry {
		ui.DryRunNotice("write " + configPath)
	} else if err := config.WriteAnnotatedWithValues(projectRoot, cfg, version); err != nil {
		return err
	}
	ui.Success(done)
	return nil
}
