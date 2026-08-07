// Package ui provides interactive terminal prompts built on charm.land/bubbletea/v2
// for enver's human-facing commands. All prompts are interactive (TTY) only.
package ui

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
