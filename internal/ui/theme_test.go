package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestThemeIconSemanticColors(t *testing.T) {
	th := defaultTheme()
	cases := []struct {
		glyph string
		code  string // standard ANSI-16 foreground code
	}{
		{IconAdd, "\x1b[32m"},        // green
		{IconDone, "\x1b[32m"},       // green
		{IconExtends, "\x1b[34m"},    // blue
		{IconDefault, "\x1b[33m"},    // yellow
		{IconOverride, "\x1b[35m"},   // magenta
		{IconDeleteVar, "\x1b[31m"},  // red
		{IconDeleteProf, "\x1b[31m"}, // red
		{IconBack, "\x1b[36m"},       // cyan
	}
	for _, c := range cases {
		got := th.icon(c.glyph)
		if !strings.Contains(got, c.code) {
			t.Errorf("icon(%q) = %q, want foreground code %q", c.glyph, got, c.code)
		}
		if w := lipgloss.Width(got); w != 1 {
			t.Errorf("icon(%q) visible width = %d, want 1 (ANSI must not affect layout)", c.glyph, w)
		}
	}
}

func TestThemeIconUnknownPassthrough(t *testing.T) {
	th := defaultTheme()
	if got := th.icon("?"); got != "?" {
		t.Errorf("icon(\"?\") = %q, want passthrough \"?\"", got)
	}
}
