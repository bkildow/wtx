// Package main is the entry point for the wtx CLI.
package main

import (
	"errors"
	"os"

	"github.com/bkildow/wtx/cmd"
	"github.com/bkildow/wtx/internal/ui"
)

func main() {
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, cmd.ErrReported) {
			ui.Error(err.Error())
		}
		os.Exit(1)
	}
}
