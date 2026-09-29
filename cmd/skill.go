package cmd

import (
	"fmt"

	"github.com/bkildow/wtx/internal/skill"
	"github.com/spf13/cobra"
)

func newSkillCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "skill",
		Short: "Print the wtx agent skill (for AI coding agents)",
		Long:  "Outputs the agent skill that teaches AI coding agents how to use wtx.\nThe installable skill (npx skills add bkildow/wtx) is a thin wrapper that runs this command,\nso the instructions always match the installed wtx version.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Print(skill.Content)
			return nil
		},
	}
}
