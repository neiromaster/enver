// Package ui provides interactive terminal prompts built on charm.land/bubbletea/v2
// for enver's human-facing commands. All prompts are interactive (TTY) only.
package ui

// EnvEntry is one environment variable as collected or edited interactively.
type EnvEntry struct {
	Key     string
	Value   string
	Comment string
	// Secret reports that the entry carries the secret mark. Key stays the
	// runtime name: the mark is file syntax, never part of the name.
	Secret bool
	// Encrypted reports that the entry's value is sealed at rest. The editor
	// loads such a value decrypted for the card and seals it again on save; no
	// text field can express the state, so it rides along like Secret.
	Encrypted bool
}

// Option is one selectable item in a Select prompt.
type Option struct {
	Value string
	Label string
	Icon  string
	// Action marks a command row (e.g. Back) rather than a selectable choice. In
	// a multi-select it renders without a checkbox, is never toggled (nor affected
	// by select-all), and pressing Enter on it aborts the session, so a command
	// such as Back can cancel without touching the checked set.
	Action bool
	// Dim renders the whole row faded (faint) while keeping it selectable: the
	// state that dimming stands for must stay visible, just not shout. The
	// cursor row ignores Dim so navigation stays crisp.
	Dim       bool
	Separator bool
}

func Separator() Option { return Option{Separator: true} }
