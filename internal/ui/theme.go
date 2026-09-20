package ui

import (
	"fmt"
	"sort"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	catppuccin "github.com/catppuccin/go"

	"charm.land/huh/v2"
)

// theme holds the semantic colors used throughout wtx output.
// Accent and Heading are optional; when empty they fall back to Info.
type theme struct {
	Success string
	Error   string
	Warning string
	Info    string
	Muted   string
	Accent  string // interactive elements (caret, cursor, buttons); defaults to Info
	Heading string // headings and paths; defaults to Info
}

// accent returns the theme's Accent color, falling back to Info.
func (t theme) accent() string {
	if t.Accent != "" {
		return t.Accent
	}
	return t.Info
}

// heading returns the theme's Heading color, falling back to Info.
func (t theme) heading() string {
	if t.Heading != "" {
		return t.Heading
	}
	return t.Info
}

// catppuccinTheme maps a catppuccin flavor to our five semantic colors.
func catppuccinTheme(f catppuccin.Flavor) theme {
	return theme{
		Success: f.Green().Hex,
		Error:   f.Red().Hex,
		Warning: f.Yellow().Hex,
		Info:    f.Sapphire().Hex,
		Muted:   f.Overlay0().Hex,
	}
}

// themes is the built-in theme registry.
var themes = map[string]theme{
	"default": {
		Success: "#22c55e",
		Error:   "#ef4444",
		Warning: "#eab308",
		Info:    "#3b82f6",
		Muted:   "#6b7280",
	},
	"catppuccin-mocha":     catppuccinTheme(catppuccin.Mocha),
	"catppuccin-latte":     catppuccinTheme(catppuccin.Latte),
	"catppuccin-frappe":    catppuccinTheme(catppuccin.Frappe),
	"catppuccin-macchiato": catppuccinTheme(catppuccin.Macchiato),
	"snazzy": {
		Success: "#5af78e",
		Error:   "#ff5c57",
		Warning: "#f3f99d",
		Info:    "#57c7ff",
		Muted:   "#686868",
		Accent:  "#ff6ac1",
		Heading: "#f1f1f0",
	},
	"dracula": {
		Success: "#50fa7b",
		Error:   "#ff5555",
		Warning: "#f1fa8c",
		Info:    "#8be9fd",
		Muted:   "#6272a4",
		Accent:  "#bd93f9",
		Heading: "#f8f8f2",
	},
	"nord": {
		Success: "#a3be8c",
		Error:   "#bf616a",
		Warning: "#ebcb8b",
		Info:    "#88c0d0",
		Muted:   "#4c566a",
	},
	"gruvbox": {
		Success: "#b8bb26",
		Error:   "#fb4934",
		Warning: "#fabd2f",
		Info:    "#83a598",
		Muted:   "#928374",
		Accent:  "#fe8019",
		Heading: "#ebdbb2",
	},
}

const defaultTheme = "snazzy"

// activeTheme tracks the currently applied theme name for WtxTheme.
var activeTheme = defaultTheme

func init() {
	// "default" is an alias for snazzy so existing configs/env vars still work.
	themes["default"] = themes[defaultTheme]
}

// ApplyTheme looks up a theme by name and reassigns all Color*/Style* package
// vars. Unknown names print a warning and fall back to "default".
func ApplyTheme(name string) {
	t, ok := themes[name]
	if !ok {
		fmt.Fprintf(Output, "wtx: unknown theme %q (available: %s)\n",
			name, strings.Join(ThemeNames(), ", "))
		name = defaultTheme
		t = themes[name]
	}

	activeTheme = name

	ColorSuccess = lipgloss.Color(t.Success)
	ColorError = lipgloss.Color(t.Error)
	ColorWarning = lipgloss.Color(t.Warning)
	ColorInfo = lipgloss.Color(t.Info)
	ColorMuted = lipgloss.Color(t.Muted)

	heading := lipgloss.Color(t.heading())

	StyleSuccess = lipgloss.NewStyle().Foreground(ColorSuccess)
	StyleError = lipgloss.NewStyle().Foreground(ColorError)
	StyleWarning = lipgloss.NewStyle().Foreground(ColorWarning)
	StyleInfo = lipgloss.NewStyle().Foreground(ColorInfo)
	StyleMuted = lipgloss.NewStyle().Foreground(ColorMuted)
	StyleCommand = lipgloss.NewStyle().Foreground(ColorMuted).Italic(true)
	StylePath = lipgloss.NewStyle().Foreground(heading).Bold(true)
	StyleHeading = lipgloss.NewStyle().Foreground(heading).Bold(true)
}

// ThemeNames returns a sorted list of available theme names.
func ThemeNames() []string {
	names := make([]string, 0, len(themes))
	for n := range themes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// wtxThemeCache implements huh.Theme with per-isDark caching so the ~70 style
// operations in buildStyles are not repeated on every render frame.
type wtxThemeCache struct {
	name  string      // theme name at creation time
	light *huh.Styles // cached for isDark=false
	dark  *huh.Styles // cached for isDark=true
}

func (c *wtxThemeCache) Theme(isDark bool) *huh.Styles {
	if isDark {
		if c.dark == nil {
			c.dark = buildStyles(c.name, true)
		}
		return c.dark
	}
	if c.light == nil {
		c.light = buildStyles(c.name, false)
	}
	return c.light
}

// WtxTheme returns a huh theme customised to match the active wtx palette.
func WtxTheme() huh.Theme {
	return &wtxThemeCache{name: activeTheme}
}

func buildStyles(themeName string, isDark bool) *huh.Styles {
	t := huh.ThemeCharm(isDark)

	active := themes[themeName]
	accent := lipgloss.Color(active.accent())
	heading := lipgloss.Color(active.heading())
	errC := lipgloss.Color(active.Error)
	success := lipgloss.Color(active.Success)
	muted := lipgloss.Color(active.Muted)

	// --- Focused field styles ---

	// Titles and descriptions.
	t.Focused.Title = t.Focused.Title.Foreground(heading)
	t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(heading)
	t.Focused.Directory = t.Focused.Directory.Foreground(heading)
	t.Focused.Description = t.Focused.Description.Foreground(muted)

	// Select cursor / caret.
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(accent)
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(accent)
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(muted)
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(muted)

	// Selected items (checkmark + text).
	t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(success)
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.Foreground(success)
	t.Focused.UnselectedPrefix = t.Focused.UnselectedPrefix.Foreground(muted)

	// Text input cursor and prompt.
	t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(accent)
	t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(accent)
	t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(muted)

	// Validation errors.
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(errC)
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(errC)

	// Buttons.
	t.Focused.FocusedButton = t.Focused.FocusedButton.Background(accent)
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(muted)

	// --- Blurred field styles (unfocused fields in multi-field forms) ---

	t.Blurred.Title = t.Blurred.Title.Foreground(muted)
	t.Blurred.Description = t.Blurred.Description.Foreground(muted)
	t.Blurred.SelectSelector = t.Blurred.SelectSelector.Foreground(muted)
	t.Blurred.SelectedOption = t.Blurred.SelectedOption.Foreground(muted)
	t.Blurred.SelectedPrefix = t.Blurred.SelectedPrefix.Foreground(muted)
	t.Blurred.UnselectedPrefix = t.Blurred.UnselectedPrefix.Foreground(muted)
	t.Blurred.TextInput.Prompt = t.Blurred.TextInput.Prompt.Foreground(muted)
	t.Blurred.TextInput.Placeholder = t.Blurred.TextInput.Placeholder.Foreground(muted)

	// --- Group styles ---

	t.Group.Title = t.Focused.Title
	t.Group.Description = t.Focused.Description

	return t
}
