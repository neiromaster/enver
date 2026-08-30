package main

import (
	"io"
	"testing"
)

// TestColorEnabled pins the color gate: styling is emitted only onto a
// terminal, and never under NO_COLOR (https://no-color.org, empty string
// counts as absent) or TERM=dumb.
func TestColorEnabled(t *testing.T) {
	orig := writerIsTTY
	t.Cleanup(func() { writerIsTTY = orig })

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	writerIsTTY = func(io.Writer) bool { return true }
	if !colorEnabled(io.Discard) {
		t.Error("empty NO_COLOR on a terminal must keep color enabled")
	}

	t.Setenv("NO_COLOR", "1")
	if colorEnabled(io.Discard) {
		t.Error("NO_COLOR set but colorEnabled() = true")
	}

	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if colorEnabled(io.Discard) {
		t.Error("TERM=dumb but colorEnabled() = true")
	}

	t.Setenv("TERM", "xterm-256color")
	writerIsTTY = func(io.Writer) bool { return false }
	if colorEnabled(io.Discard) {
		t.Error("destination is not a terminal but colorEnabled() = true")
	}
}
