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
		{IconAdd, "32m"},        // green
		{IconDone, "32m"},       // green
		{IconExtends, "34m"},    // blue
		{IconDefault, "33m"},    // yellow
		{IconOverride, "35m"},   // magenta
		{IconDeleteVar, "31m"},  // red
		{IconDeleteProf, "31m"}, // red
		{IconBack, "36m"},       // cyan
		{IconUnset, "31m"},      // red
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

func TestThemeStaticGlyphColors(t *testing.T) {
	th := defaultTheme()
	if !strings.Contains(th.cursor, "\x1b[36m") || !strings.Contains(th.cursor, "▸") {
		t.Errorf("cursor = %q, want cyan ▸", th.cursor)
	}
	if !strings.Contains(th.checkOn, "\x1b[32m") {
		t.Errorf("checkOn = %q, want green ●", th.checkOn)
	}
	if !strings.Contains(th.checkOff, "\x1b[2m") {
		t.Errorf("checkOff = %q, want faint ○", th.checkOff)
	}
}

func TestThemeTitleAndActiveAreCyanBold(t *testing.T) {
	th := defaultTheme()
	for _, out := range []string{th.title.Render("X"), th.active.Render("Y")} {
		if !strings.Contains(out, "36m") {
			t.Errorf("want cyan foreground, got %q", out)
		}
	}
}

// active must NOT set a background: a fixed background (e.g. the old
// Color("237") grey) ignores the terminal theme and breaks on some themes.
func TestThemeActiveHasNoBackground(t *testing.T) {
	th := defaultTheme()
	out := th.active.Render("row")
	if strings.Contains(out, "\x1b[48") { // 48 = set background color (256/truecolor)
		t.Errorf("active must not set a background (cross-theme safe): %q", out)
	}
}
