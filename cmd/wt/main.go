// Package main provides the deprecated wt entry point for wtx.
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/bkildow/wtx/cmd"
	"github.com/bkildow/wtx/internal/ui"
)

func main() {
	if os.Getenv("WTX_NO_DEPRECATION_WARN") == "" {
		fmt.Fprintln(os.Stderr, "wt is deprecated; use wtx instead. The wt command will be removed in v1.0.0.")
	}
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, cmd.ErrReported) {
			ui.Error(err.Error())
		}
		os.Exit(1)
	}
}
