// Package ui provides interactive terminal prompts built on charm.land/huh
// for enver's human-facing commands. All prompts are interactive (TTY) only.
package ui

import (
	"charm.land/huh/v2"
)

// EnvEntry is one environment variable as collected or edited interactively.
type EnvEntry struct {
	Key     string
	Value   string
	Comment string
}

// Option is one selectable item in a Select prompt.
type Option struct {
	Value     string
	Label     string
	Separator bool
}

func Separator() Option { return Option{Separator: true} }

// Input prompts for a single line of text.
func Input(title string) (string, error) {
	var v string
	err := huh.NewInput().Title(title).Value(&v).Run()
	return v, err
}

// Confirm prompts for a yes/no answer. defaultYes sets the initial selection.
func Confirm(title string, defaultYes bool) (bool, error) {
	v := defaultYes
	err := huh.NewConfirm().Title(title).Value(&v).Run()
	return v, err
}

// EnvCard prompts for one environment variable: name, value, and an optional
// comment. entry pre-fills the fields (empty for a new variable). A blank name
// signals "finished" to the caller.
func EnvCard(entry EnvEntry) (EnvEntry, error) {
	e := entry
	err := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Variable name (blank to finish)").Value(&e.Key),
		huh.NewInput().Title("Value").Value(&e.Value),
		huh.NewInput().Title("Comment (optional)").Value(&e.Comment),
	)).Run()
	return e, err
}
