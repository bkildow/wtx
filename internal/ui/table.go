package ui

import (
	lipgloss "charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// NewTable returns a lipgloss table pre-configured with the wtx visual style.
// Callers should chain .Headers(...) and .Row(...) / .Rows(...) on the result.
func NewTable() *table.Table {
	headerStyle := StyleHeading.Padding(0, 1)

	cellStyle := lipgloss.NewStyle().Padding(0, 1)

	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(ColorMuted)).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return headerStyle
			}
			return cellStyle
		})
}

// PrintTable renders the given table to ui.Output (stderr).
func PrintTable(t *table.Table) {
	_, _ = lipgloss.Fprintln(Output, t)
}
