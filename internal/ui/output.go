// Package ui provides terminal output helpers and interactive prompts.
package ui

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	lipgloss "charm.land/lipgloss/v2"
)

var (
	Output  io.Writer = os.Stderr
	Verbose bool

	// outputMu keeps lines written from concurrent goroutines whole.
	outputMu sync.Mutex
)

func writeLine(line string) {
	outputMu.Lock()
	defer outputMu.Unlock()
	_, _ = lipgloss.Fprintln(Output, line)
}

func Success(msg string) {
	writeLine(StyleSuccess.Render("✓ " + msg))
}

func Error(msg string) {
	writeLine(StyleError.Render("✗ " + msg))
}

func Warning(msg string) {
	writeLine(StyleWarning.Render("⚠ " + msg))
}

func Info(msg string) {
	writeLine(StyleInfo.Render(msg))
}

func Step(msg string) {
	writeLine(StyleInfo.Render("→ " + msg))
}

func DryRunNotice(action string) {
	writeLine(StyleMuted.Render("[dry-run] " + action))
}

func Command(cmd string) {
	if !Verbose {
		return
	}
	writeLine(StyleCommand.Render("  $ " + cmd))
}

func Heading(msg string) {
	writeLine(StyleHeading.Render(msg))
}

// FormatBytes formats a byte count into a human-friendly base-1024 string
// like "820 MB" or "9.4 GB". Values are shown with one decimal place below
// 10 units and rounded to whole units above that.
func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}

	value := float64(b)
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	var suffix string
	for _, u := range units {
		value /= unit
		suffix = u
		if value < unit {
			break
		}
	}

	if value < 10 {
		return fmt.Sprintf("%.1f %s", value, suffix)
	}
	return fmt.Sprintf("%.0f %s", value, suffix)
}

// FormatDuration formats a duration into a human-friendly string like
// "45 seconds" or "2 minutes 30 seconds".
func FormatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	s := int(d.Seconds())
	if s < 60 {
		if s == 1 {
			return "1 second"
		}
		return fmt.Sprintf("%d seconds", s)
	}
	m := s / 60
	rem := s % 60
	mUnit := "minutes"
	if m == 1 {
		mUnit = "minute"
	}
	if rem == 0 {
		return fmt.Sprintf("%d %s", m, mUnit)
	}
	sUnit := "seconds"
	if rem == 1 {
		sUnit = "second"
	}
	return fmt.Sprintf("%d %s %d %s", m, mUnit, rem, sUnit)
}
