package main

import (
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// dimStyle and boldStyle carry the CLI text theme, mirroring the internal/ui
// one: comments fade back, keys carry the emphasis. Values stay plain so
// they read and copy cleanly.
var (
	dimStyle  = lipgloss.NewStyle().Faint(true)
	boldStyle = lipgloss.NewStyle().Bold(true)
)

// writerIsTTY reports whether w is an interactive terminal. It is a var so
// tests can force either side without a pty.
var writerIsTTY = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// colorEnabled reports whether text output may emit ANSI styling onto w:
// only onto a terminal, and never under NO_COLOR (https://no-color.org,
// empty counts as absent) or TERM=dumb. The decision follows the writer
// itself, so any redirection of the command's output stays escape-free even
// when the process stdout is a terminal.
func colorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return writerIsTTY(w)
}
