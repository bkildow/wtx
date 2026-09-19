// Package main is the entry point for the wtx CLI.
package main

import (
	"os"

	"github.com/bkildow/wtx/cmd"
	"github.com/bkildow/wtx/internal/ui"
)

func main() {
	if err := cmd.Execute(); err != nil {
		ui.Error(err.Error())
		os.Exit(1)
	}
}
