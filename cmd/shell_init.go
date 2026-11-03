package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const bashFunction = `wtx() {
  if [ "$1" = "cd" ] || [ "$1" = "add" ] || [ "$1" = "root" ] || [ "$1" = "remove" ]; then
    local dir
    dir="$(command wtx "$@")" || return
    if [ -n "$dir" ]; then
      cd "$dir" || return
    fi
  else
    command wtx "$@"
  fi
}
`

const zshFunction = `unalias wtx 2>/dev/null
eval 'wtx() {
  if [ "$1" = "cd" ] || [ "$1" = "add" ] || [ "$1" = "root" ] || [ "$1" = "remove" ]; then
    local dir
    dir="$(command wtx "$@")" || return
    if [ -n "$dir" ]; then
      cd "$dir" || return
    fi
  else
    command wtx "$@"
  fi
}'
`

const fishFunction = `function wtx
  if test "$argv[1]" = "cd" -o "$argv[1]" = "add" -o "$argv[1]" = "root" -o "$argv[1]" = "remove"
    set -l dir (command wtx $argv)
    or return $status
    if test -n "$dir"
      cd "$dir"
    end
  else
    command wtx $argv
  end
end
`

func newShellInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:       "shell-init [bash|zsh|fish]",
		Short:     "Print shell startup configuration (wrapper function + completions)",
		Long:      "Outputs shell code that sets up the wtx directory-changing wrapper function and tab completions. Add to your shell config with eval.",
		ValidArgs: []string{"bash", "zsh", "fish"},
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Print the wrapper function, then the completions.
			root := cmd.Root()
			switch args[0] {
			case "bash":
				fmt.Print(bashFunction)
				return root.GenBashCompletionV2(os.Stdout, true)
			case "zsh":
				fmt.Print(zshFunction)
				return root.GenZshCompletion(os.Stdout)
			default: // fish; Args admits only ValidArgs
				fmt.Print(fishFunction)
				return root.GenFishCompletion(os.Stdout, true)
			}
		},
	}
}
