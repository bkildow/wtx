package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

const bashFunction = `wt() {
  if [ "$1" = "cd" ] || [ "$1" = "add" ] || [ "$1" = "root" ] || [ "$1" = "remove" ]; then
    local dir
    dir="$(command wt "$@")" || return
    if [ -n "$dir" ]; then
      cd "$dir" || return
    fi
  else
    command wt "$@"
  fi
}
`

const zshFunction = `unalias wt 2>/dev/null
eval 'wt() {
  if [ "$1" = "cd" ] || [ "$1" = "add" ] || [ "$1" = "root" ] || [ "$1" = "remove" ]; then
    local dir
    dir="$(command wt "$@")" || return
    if [ -n "$dir" ]; then
      cd "$dir" || return
    fi
  else
    command wt "$@"
  fi
}'
`

const fishFunction = `function wt
  if test "$argv[1]" = "cd" -o "$argv[1]" = "add" -o "$argv[1]" = "root" -o "$argv[1]" = "remove"
    set -l dir (command wt $argv)
    or return $status
    if test -n "$dir"
      cd "$dir"
    end
  else
    command wt $argv
  end
end
`

func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "shell-init [bash|zsh|fish]",
		Short:     "Print shell startup configuration (wrapper function + completions)",
		Long:      "Outputs shell code that sets up the wtx and deprecated wt directory-changing wrapper functions and tab completions. Add to your shell config with eval.",
		ValidArgs: []string{"bash", "zsh", "fish"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			shell := args[0]

			// Print wrapper function
			switch shell {
			case "bash":
				fmt.Print(strings.ReplaceAll(bashFunction, "wt", "wtx"), bashFunction)
			case "zsh":
				fmt.Print(strings.ReplaceAll(zshFunction, "wt", "wtx"), zshFunction)
			case "fish":
				fmt.Print(strings.ReplaceAll(fishFunction, "wt", "wtx"), fishFunction)
			}

			// Print completions
			switch shell {
			case "bash":
				if err := cmd.Root().GenBashCompletionV2(os.Stdout, true); err != nil {
					return err
				}
				fmt.Println("complete -o default -F __start_wtx wt")
			case "zsh":
				if err := cmd.Root().GenZshCompletion(os.Stdout); err != nil {
					return err
				}
				fmt.Println("compdef _wtx wt")
			case "fish":
				if err := cmd.Root().GenFishCompletion(os.Stdout, true); err != nil {
					return err
				}
				fmt.Println("complete -c wt -w wtx")
			}

			return nil
		},
	}
}
