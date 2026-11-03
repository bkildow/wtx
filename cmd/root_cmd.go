package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "root",
		Short: "Print project root path for shell navigation",
		Long:  "Prints the absolute path to the wtx project root (the directory containing .worktree.yml).",
		Args:  cobra.NoArgs,
		RunE:  runRoot,
	}
}

func runRoot(cmd *cobra.Command, args []string) error {
	projectRoot, err := findProjectRoot() // raw-load: wtx root prints only the root, so a broken config must not stop it
	if err != nil {
		return err
	}

	fmt.Println(projectRoot)
	return nil
}
