// Package main provides the deprecated wt entry point for wtx.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/bkildow/wtx/cmd"
	"github.com/bkildow/wtx/internal/ui"
)

// banner is plain text on stderr so `$(command wt ...)` shell wrappers keep a
// clean stdout. WTX_NO_DEPRECATION_WARN=1 silences it.
const banner = `────────────────────────────────────────────────────────────
⚠ wt is deprecated and will be removed in v1.0.0.
  Use wtx instead, and update your shell startup line to
  ` + "`wtx shell-init <shell>`" + `. Run ` + "`wtx doctor --user`" + ` to check.
────────────────────────────────────────────────────────────
`

func main() {
	if os.Getenv("WTX_NO_DEPRECATION_WARN") == "" {
		fmt.Fprint(os.Stderr, banner)
	}
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, cmd.ErrReported) {
			ui.Error(err.Error())
		}
		os.Exit(1)
	}
}
