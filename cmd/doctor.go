package cmd

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bkildow/wtx/internal/doctor"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

// ErrReported marks failures a command has already rendered; main exits
// unsuccessfully without printing them again.
var ErrReported = errors.New("already reported")

// ErrDoctorUnhealthy signals an unsuccessful, already-rendered report.
var ErrDoctorUnhealthy = fmt.Errorf("doctor found unsuccessful checks or repairs: %w", ErrReported)

func newDoctorCmd() *cobra.Command {
	var fix, user, structured, strict, migrateHome bool
	var name string
	cmd := &cobra.Command{
		Use: "doctor", Short: "Check project health and migration readiness",
		Long: "Inspect project health without changing files. --fix repairs managed metadata and recognized existing Claude hooks with backups. Shared files, worktrees, scripts, and user dotfiles receive manual remedies. Legacy input settings (WT_THEME, WT_NO_DISK_WARN) are no longer read; commands and script exports must migrate before v1.0. Output groups related findings; --verbose lists every finding, including passing checks.\n\n" +
			"--migrate-home moves an in-repo clone (wtx init --in-repo, or an older project with worktrees/ and shared/ at the repository root) to its clone directory ~/.wtx/<name>/: worktrees (git worktree move), shared/ and bin/, an owner marker and git config wtx.name; it removes worktree_dir, shared_dir and the bin/ scripts entries from .worktree.yml (backed up first) so the paths resolve per clone. Locked worktrees, worktrees with submodules, Git-tracked shared files and a bin/ outside worktree_dir stay put and are reported. Preview with --dry-run; rerunning resumes an interrupted migration.",
		Args: cobra.NoArgs,
		// Avoid theme/progress diagnostics in JSON mode, including --verbose.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !structured && rootCmd.PersistentPreRunE != nil {
				return rootCmd.PersistentPreRunE(cmd, args)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("name") && !migrateHome {
				return errors.New("--name requires --migrate-home")
			}
			report := doctor.Run(cmd.Context(), doctor.Options{User: user, Fix: fix, DryRun: IsDryRun(), MigrateHome: migrateHome, HomeName: name})
			if structured {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(report); err != nil {
					return err
				}
			} else {
				renderDoctorReport(ui.Output, report, ui.Verbose)
			}
			if report.Unsuccessful(strict) {
				return ErrDoctorUnhealthy
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "Apply safe repairs and check again")
	cmd.Flags().BoolVar(&user, "user", false, "Check user configuration only (manual repairs)")
	cmd.Flags().BoolVar(&structured, "json", false, "Write a structured report to stdout")
	cmd.Flags().BoolVar(&strict, "strict", false, "Exit unsuccessfully for warnings as well as failures")
	cmd.Flags().BoolVar(&migrateHome, "migrate-home", false, "Move an in-repo clone's worktrees, shared files and scripts to ~/.wtx/<name>/")
	cmd.Flags().StringVar(&name, "name", "", "Directory name under ~/.wtx for --migrate-home (default: wtx.name, else the repository directory name)")
	cmd.MarkFlagsMutuallyExclusive("fix", "migrate-home")
	return cmd
}
