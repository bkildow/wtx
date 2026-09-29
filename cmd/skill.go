package cmd

import (
	_ "embed"
	"fmt"

	"github.com/spf13/cobra"
)

// skillContent is the full agent skill (SKILL.md with frontmatter). The thin
// installable wrapper in skills/wtx/SKILL.md tells agents to run `wtx skill`,
// so this text always matches the installed binary.
//
//go:embed agent_skill.md
var skillContent string

func newSkillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "skill",
		Short: "Print the wtx agent skill (for AI coding agents)",
		Long:  "Outputs the agent skill that teaches AI coding agents how to use wtx.\nThe installable skill (npx skills add bkildow/wtx) is a thin wrapper that runs this command,\nso the instructions always match the installed wtx version.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), skillContent)
			return err
		},
	}
}
