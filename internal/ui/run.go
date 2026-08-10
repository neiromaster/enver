package ui

import (
	"errors"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
)

var ErrCanceled = errors.New("canceled")

// Interactive is a var so tests can force the non-TTY path without a pty.
var Interactive = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func run(m tea.Model) (tea.Model, error) {
	if !Interactive() {
		return m, errors.New("interactive prompt requires a terminal")
	}
	// Each prompt is its own tea.Program, whose last frame stays on screen after
	// exit. Without a gap, successive prompts (e.g. edit → pick profile → edit
	// variable) merge into one block. Print a blank line first to separate them.
	_, _ = os.Stdout.WriteString("\n")
	out, err := tea.NewProgram(m).Run()
	if err != nil {
		return m, err
	}
	if c, ok := out.(interface{ Canceled() bool }); ok && c.Canceled() {
		return out, ErrCanceled
	}
	return out, nil
}
