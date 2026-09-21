package cmd

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/bkildow/wtx/internal/doctor"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

// ErrDoctorUnhealthy signals an unsuccessful, already-rendered report.
var ErrDoctorUnhealthy = errors.New("doctor found unsuccessful checks or repairs")

func newDoctorCmd() *cobra.Command {
	var fix, user, structured, strict bool
	cmd := &cobra.Command{
		Use: "doctor", Short: "Check project health and migration readiness",
		Long: "Inspect project health without changing files. --fix repairs managed metadata and recognized existing Claude hooks with backups. Shared files, worktrees, scripts, and user dotfiles receive manual remedies. Legacy input settings must migrate before v0.12; commands and script exports before v1.0.",
		Args: cobra.NoArgs,
		// Avoid theme/progress diagnostics in JSON mode, including --verbose.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !structured && rootCmd.PersistentPreRunE != nil {
				return rootCmd.PersistentPreRunE(cmd, args)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := doctor.Run(cmd.Context(), doctor.Options{User: user, Fix: fix, DryRun: IsDryRun()})
			if structured {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				if err := encoder.Encode(report); err != nil {
					return err
				}
			} else {
				for _, f := range report.Findings {
					where := f.Path
					if f.Line > 0 {
						where += fmt.Sprintf(":%d", f.Line)
					}
					fmt.Fprintf(ui.Output, "%s %s %s: %s\n", f.Severity, f.ID, where, f.Explanation)
					if f.Remedy != "" {
						fmt.Fprintf(ui.Output, "  %s\n", f.Remedy)
					}
				}
				for _, r := range report.Repairs {
					fmt.Fprintf(ui.Output, "%s %s: %s\n", r.Status, r.ID, r.Path)
					if r.Backup != "" {
						fmt.Fprintf(ui.Output, "  Backup: %s\n", r.Backup)
					}
					if r.Error != "" {
						fmt.Fprintf(ui.Output, "  %s\n", r.Error)
					}
				}
				fmt.Fprintf(ui.Output, "%d ok, %d warnings, %d failures\n", report.Counts.OK, report.Counts.Warn, report.Counts.Fail)
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
	return cmd
}
