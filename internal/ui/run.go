package ui

import (
	"errors"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
)

var ErrCanceled = errors.New("canceled")

func run(m tea.Model) (tea.Model, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return m, errors.New("interactive prompt requires a terminal")
	}
	out, err := tea.NewProgram(m).Run()
	if err != nil {
		return m, err
	}
	if c, ok := out.(interface{ Canceled() bool }); ok && c.Canceled() {
		return out, ErrCanceled
	}
	return out, nil
}
