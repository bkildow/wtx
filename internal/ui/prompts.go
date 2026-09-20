package ui

import (
	"errors"

	"charm.land/bubbles/v2/key"
	"charm.land/huh/v2"
)

type Prompter interface {
	SelectBranch(branches []string) (string, error)
	SelectWorktree(worktrees []string) (string, error)
	SelectEditor(editors []string) (string, error)
	SelectScript(scripts []string) (string, error)
	Confirm(title string) (bool, error)
	InputString(title, placeholder string) (string, error)
}

// IsUserAbort returns true if the error is a user cancellation (ESC/ctrl+c).
func IsUserAbort(err error) bool {
	return errors.Is(err, huh.ErrUserAborted)
}

func wtKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Quit = key.NewBinding(key.WithKeys("ctrl+c", "esc"))
	return km
}

// runForm wraps a field in a standard wtx form and runs it.
func runForm(fields ...huh.Field) error {
	return huh.NewForm(huh.NewGroup(fields...)).
		WithTheme(WtxTheme()).
		WithKeyMap(wtKeyMap()).
		WithShowHelp(true).
		WithOutput(Output).
		Run()
}

func selectHeight(itemCount int) int {
	if itemCount <= 20 {
		return 0
	}
	return 22
}

func selectFromStrings(title string, items []string) (string, error) {
	var selected string
	opts := make([]huh.Option[string], len(items))
	for i, v := range items {
		opts[i] = huh.NewOption(v, v)
	}

	field := huh.NewSelect[string]().
		Title(title).
		Options(opts...)

	if h := selectHeight(len(opts)); h > 0 {
		field = field.Height(h)
	}
	field = field.Value(&selected)

	return selected, runForm(field)
}

type InteractivePrompter struct{}

func (p *InteractivePrompter) SelectBranch(branches []string) (string, error) {
	return selectFromStrings("Select a branch", branches)
}

func (p *InteractivePrompter) SelectWorktree(worktrees []string) (string, error) {
	return selectFromStrings("Select a worktree", worktrees)
}

func (p *InteractivePrompter) SelectEditor(editors []string) (string, error) {
	return selectFromStrings("Select an editor", editors)
}

func (p *InteractivePrompter) SelectScript(scripts []string) (string, error) {
	return selectFromStrings("Select a script", scripts)
}

func (p *InteractivePrompter) Confirm(title string) (bool, error) {
	var confirmed bool
	field := huh.NewConfirm().
		Title(title).
		Value(&confirmed)

	return confirmed, runForm(field)
}

func (p *InteractivePrompter) InputString(title, placeholder string) (string, error) {
	var value string
	field := huh.NewInput().
		Title(title).
		Placeholder(placeholder).
		Value(&value)

	return value, runForm(field)
}
