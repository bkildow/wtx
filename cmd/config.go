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
		Short: "Generate an annotated .worktree.yml with documentation comments",
		Args:  cobra.NoArgs,
		RunE:  runConfigInit,
	}
	cmd.Flags().Bool("update", false, "Merge existing values into the annotated template")
	return cmd
}

func runConfigInit(cmd *cobra.Command, args []string) error {
	dry := IsDryRun()
	update, _ := cmd.Flags().GetBool("update")

	projectRoot, err := findProjectRoot()
	if err != nil {
		return err
	}

	configPath := filepath.Join(projectRoot, config.ConfigFileName)

	if config.Exists(projectRoot) {
		if update {
			// --update: load existing config, write annotated template with those values
			existing, err := config.Load(projectRoot)
			if err != nil {
				return fmt.Errorf("failed to load existing config: %w", err)
			}

			ui.Step("Updating " + config.ConfigFileName + " with documentation comments")
			if dry {
				ui.DryRunNotice("write " + configPath)
			} else {
				if err := config.WriteAnnotatedWithValues(projectRoot, existing); err != nil {
					return err
				}
			}
			ui.Success("Updated " + config.ConfigFileName + " with documentation comments")
		} else {
			// Default: backup existing, write fresh template
			backupPath := configPath + ".bak"

			ui.Step("Backing up existing config to " + config.ConfigFileName + ".bak")
			if dry {
				ui.DryRunNotice("cp " + configPath + " " + backupPath)
			} else {
				data, err := os.ReadFile(configPath)
				if err != nil {
					return fmt.Errorf("failed to read existing config: %w", err)
				}
				if err := os.WriteFile(backupPath, data, 0o644); err != nil {
					return fmt.Errorf("failed to write backup: %w", err)
				}
			}

			ui.Step("Writing fresh " + config.ConfigFileName)
			if dry {
				ui.DryRunNotice("write " + configPath)
			} else {
				if err := config.WriteAnnotated(projectRoot); err != nil {
					return err
				}
			}
			ui.Success("Backed up existing config to " + config.ConfigFileName + ".bak")
		}
	} else {
		// No existing config: write fresh annotated template
		ui.Step("Writing " + config.ConfigFileName)
		if dry {
			ui.DryRunNotice("write " + configPath)
		} else {
			if err := config.WriteAnnotated(projectRoot); err != nil {
				return err
			}
		}
		ui.Success("Created " + config.ConfigFileName)
	}

	return nil
}
