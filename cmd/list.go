package cmd

import (
	"cmp"

	"github.com/bkildow/wtx/internal/project"
	"github.com/bkildow/wtx/internal/ui"
	"github.com/spf13/cobra"
)

func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all worktrees",
		Args:  cobra.NoArgs,
		RunE:  runList,
	}
	cmd.Flags().Bool("json", false, "Write worktrees as JSON to stdout")
	return cmd
}

// listReport is the --json output of wtx list. Adding fields is compatible;
// renaming or removing one needs a SchemaVersion bump.
type listReport struct {
	SchemaVersion int         `json:"schema_version"`
	ProjectRoot   string      `json:"project_root"`
	Worktrees     []listEntry `json:"worktrees"`
}

type listEntry struct {
	Branch      string `json:"branch"` // "" for a detached HEAD
	Path        string `json:"path"`
	Head        string `json:"head"`
	ProjectRoot bool   `json:"project_root"`
}

func runList(cmd *cobra.Command, args []string) error {
	clone, err := openClone(cmd.Context())
	if err != nil {
		return err
	}
	projectRoot := clone.Root()

	wts, err := clone.Worktrees(cmd.Context())
	if err != nil {
		return err
	}
	entries := make([]listEntry, 0, len(wts.Managed)+1)
	// In a Checkout or In-repo layout the Main worktree is the Project root,
	// not a Managed worktree, so it gets its own row.
	if clone.Layout() != project.BareLayout && wts.HasMain {
		entries = append(entries, listEntry{Branch: wts.Main.Branch, Path: projectRoot, Head: wts.Main.Head, ProjectRoot: true})
	}
	for _, wt := range wts.Managed {
		entries = append(entries, listEntry{Branch: wt.Branch, Path: wt.Path, Head: wt.Head})
	}

	if structured, _ := cmd.Flags().GetBool("json"); structured {
		return writeJSON(cmd.OutOrStdout(), listReport{SchemaVersion: 1, ProjectRoot: projectRoot, Worktrees: entries})
	}

	if len(entries) == 0 {
		ui.Info("No worktrees found. Use 'wtx add' to create one.")
		return nil
	}

	ui.Heading("Worktrees")

	t := ui.NewTable().Headers("BRANCH", "PATH", "COMMIT")
	paths := ui.NewPathDisplay(projectRoot)
	for _, entry := range entries {
		if !entry.ProjectRoot {
			t.Row(entry.Branch, paths.Path(entry.Path), shortHead(entry.Head))
			continue
		}
		branch := cmp.Or(entry.Branch, "(detached)")
		t.Row(branch, ui.DisplayPath("", projectRoot)+" (project root)", shortHead(entry.Head))
	}
	ui.PrintTable(t)
	return nil
}

// shortHead abbreviates a commit hash to 7 characters.
func shortHead(head string) string {
	if len(head) > 7 {
		return head[:7]
	}
	return head
}
