package cmd

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/bkildow/wtx/internal/git"
	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show status of all worktrees",
		Args:  cobra.NoArgs,
		RunE:  runStatus,
	}
	cmd.Flags().Bool("json", false, "Write worktree status as JSON to stdout")
	return cmd
}

// statusReport is the --json output of wtx status. Adding fields is
// compatible; renaming or removing one needs a SchemaVersion bump.
type statusReport struct {
	SchemaVersion int           `json:"schema_version"`
	ProjectRoot   string        `json:"project_root"`
	Worktrees     []statusEntry `json:"worktrees"`
}

// statusEntry is one Managed worktree's status. Dirty and LastCommitAge are
// nil when git could not read them, and Error says why. Upstream is nil when
// the branch has none; Ahead and Behind are nil then and when it is gone.
type statusEntry struct {
	Branch        string              `json:"branch"`
	Path          string              `json:"path"`
	Head          string              `json:"head"`
	Dirty         *bool               `json:"dirty"`
	Upstream      *string             `json:"upstream"`
	Ahead         *int                `json:"ahead"`
	Behind        *int                `json:"behind"`
	LastCommitAge *string             `json:"last_commit_age"`
	Setup         *project.SetupState `json:"setup"`
	Error         string              `json:"error,omitempty"`

	// The table renders the upstream lookup itself: a failed lookup and a
	// missing upstream both leave Upstream nil.
	upstream    git.UpstreamStatus
	upstreamErr error
}

func runStatus(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	clone, err := openClone(ctx)
	if err != nil {
		return err
	}
	projectRoot, cfg := clone.Root(), clone.Config()

	runner := clone.Runner()
	filtered, err := clone.ManagedWorktrees(ctx)
	if err != nil {
		return err
	}

	entries := make([]statusEntry, 0, len(filtered))
	for _, wt := range filtered {
		entry := statusEntry{Branch: wt.Branch, Path: wt.Path, Head: wt.Head}

		// An unreadable worktree keeps its entry, with the failing fields
		// nil and the reason in Error, rather than failing the command.
		dirty, dirtyErr := runner.IsWorktreeDirty(ctx, wt.Path)
		if dirtyErr == nil {
			entry.Dirty = &dirty
		}
		age, ageErr := runner.GetLastCommitAge(ctx, wt.Path)
		if ageErr == nil {
			entry.LastCommitAge = &age
		}
		entry.upstream, entry.upstreamErr = runner.UpstreamStatus(ctx, wt.Path, wt.Branch)
		if up := entry.upstream; entry.upstreamErr == nil && up.Upstream != "" {
			entry.Upstream = &up.Upstream
			if !up.Gone {
				entry.Ahead, entry.Behind = &up.Ahead, &up.Behind
			}
		}
		if gitErr := cmp.Or(dirtyErr, ageErr, entry.upstreamErr); gitErr != nil {
			entry.Error = gitErrorReason(gitErr)
		}

		// An unreadable state is reported like a missing one. The recorded
		// log path goes stale when the worktree moves, so report where the
		// log is now, or "" when there is none.
		if entry.Setup, _ = project.ResolveSetupStatus(wt.Path); entry.Setup != nil {
			entry.Setup.LogFile = project.SetupLog(wt.Path, entry.Setup)
		}
		entries = append(entries, entry)
	}

	if structured, _ := cmd.Flags().GetBool("json"); structured {
		return writeJSON(cmd.OutOrStdout(), statusReport{SchemaVersion: 1, ProjectRoot: projectRoot, Worktrees: entries})
	}

	if len(entries) == 0 {
		ui.Info("No worktrees found. Use 'wtx add' to create one.")
		warnLowDisk(projectRoot, cfg)
		return nil
	}

	ui.Heading("Worktree Status")

	t := ui.NewTable().Headers("BRANCH", "PATH", "COMMIT", "STATUS", "UPSTREAM", "SETUP", "LAST COMMIT")
	paths := ui.NewPathDisplay(projectRoot)
	var hints []string
	for _, entry := range entries {
		styledStatus := ui.StyleSuccess.Render("clean")
		switch {
		case entry.Dirty == nil:
			styledStatus = ui.StyleError.Render("error")
		case *entry.Dirty:
			styledStatus = ui.StyleWarning.Render("dirty")
		}

		age := ui.StyleError.Render("error")
		if entry.LastCommitAge != nil {
			age = *entry.LastCommitAge
		}

		if entry.Error != "" {
			hints = append(hints, "Could not read status for "+entry.Branch+": "+entry.Error)
		}
		if entry.Setup != nil && entry.Setup.Status == project.SetupFailed {
			hints = append(hints, setupFailureHint(entry.Branch, entry.Path, entry.Setup))
		}

		t.Row(entry.Branch, paths.Path(entry.Path), shortHead(entry.Head), styledStatus, renderUpstream(entry.Branch, entry.upstream, entry.upstreamErr), renderSetupStatus(entry.Setup), age)
	}
	ui.PrintTable(t)
	for _, hint := range hints {
		ui.Warning(hint)
	}
	warnLowDisk(projectRoot, cfg)
	return nil
}

// setupFailureHint tells the user where to look after a failed setup. Only a
// background run has a log; a foreground run printed its output to the terminal.
func setupFailureHint(branch, worktreePath string, state *project.SetupState) string {
	hint := "Setup failed for " + branch
	if firstLine, _, _ := strings.Cut(state.Error, "\n"); firstLine != "" {
		hint += ": " + firstLine
	}
	if project.SetupLog(worktreePath, state) != "" {
		return hint + " — run 'wtx logs " + branch + "'"
	}
	return hint + " — re-run with 'wtx setup " + branch + " --foreground'"
}

// gitErrorReason explains a git failure in one line. Runner errors put the
// command first and git's stderr after it, so a "fatal:" line is preferred.
func gitErrorReason(err error) string {
	msg := err.Error()
	for line := range strings.Lines(msg) {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "fatal:") {
			return line
		}
	}
	reason, _, _ := strings.Cut(msg, "\n")
	return reason
}

// renderUpstream shows the commits ahead (↑) and behind (↓) the upstream, or
// "=" when even, naming the upstream when it isn't origin/<branch>.
func renderUpstream(branch string, up git.UpstreamStatus, err error) string {
	switch {
	case err != nil:
		return ui.StyleError.Render("error")
	case up.Upstream == "":
		return ui.StyleMuted.Render("-")
	}

	var cell string
	switch {
	case up.Gone:
		cell = ui.StyleWarning.Render("gone")
	case up.Ahead == 0 && up.Behind == 0:
		cell = ui.StyleSuccess.Render("=")
	default:
		var counts []string
		if up.Ahead > 0 {
			counts = append(counts, fmt.Sprintf("↑%d", up.Ahead))
		}
		if up.Behind > 0 {
			counts = append(counts, fmt.Sprintf("↓%d", up.Behind))
		}
		cell = strings.Join(counts, " ")
	}
	if up.Upstream != "origin/"+branch {
		cell += " " + ui.StyleMuted.Render("("+up.Upstream+")")
	}
	return cell
}

func renderSetupStatus(state *project.SetupState) string {
	if state == nil {
		return ui.StyleMuted.Render("-")
	}

	switch state.Status {
	case project.SetupRunning:
		return ui.StyleInfo.Render("In Progress")
	case project.SetupComplete:
		return ui.StyleSuccess.Render("Complete")
	case project.SetupSkipped:
		return ui.StyleWarning.Render("Skipped")
	case project.SetupFailed:
		return ui.StyleError.Render("Failed")
	default:
		return ui.StyleMuted.Render("-")
	}
}
