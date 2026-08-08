package ui

import (
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestEnvCardBlankNameFinishes(t *testing.T) {
	m := newEnvCardModel(EnvEntry{})
	mm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	em := mm.(*envCardModel)
	if em.cursor != 0 {
		t.Fatalf("blank name should not advance cursor, got %d", em.cursor)
	}
	if em.result().Key != "" {
		t.Fatalf("blank name should yield empty key, got %q", em.result().Key)
	}
}

func updCard(m *envCardModel, msg tea.Msg) *envCardModel {
	mm, _ := m.Update(msg)
	return mm.(*envCardModel)
}

func TestEnvCardAdvancesAndSubmits(t *testing.T) {
	m := newEnvCardModel(EnvEntry{})
	m = updCard(m, tea.KeyPressMsg{Text: "K"})          // type into name
	m = updCard(m, tea.KeyPressMsg{Code: tea.KeyTab})   // name -> value
	m = updCard(m, tea.KeyPressMsg{Text: "V"})          // type into value
	m = updCard(m, tea.KeyPressMsg{Code: tea.KeyTab})   // value -> comment
	m = updCard(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // submit on last
	if !m.submitted {
		t.Fatal("enter on last field should submit")
	}
	r := m.result()
	if r.Key != "K" || r.Value != "V" {
		t.Fatalf("result = %+v, want Key=K Value=V", r)
	}
}

func TestEnvCardCancel(t *testing.T) {
	m := newEnvCardModel(EnvEntry{})
	mm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !mm.(*envCardModel).canceled {
		t.Fatal("esc did not cancel")
	}
}

func TestEnvCardShiftTabNavigatesBack(t *testing.T) {
	m := newEnvCardModel(EnvEntry{})
	m = updCard(m, tea.KeyPressMsg{Text: "K"})        // type into name
	m = updCard(m, tea.KeyPressMsg{Code: tea.KeyTab}) // name -> value
	if m.cursor != 1 {
		t.Fatalf("cursor should be at value (1), got %d", m.cursor)
	}
	m = updCard(m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}) // value -> name (back)
	if m.cursor != 0 {
		t.Fatalf("shift+tab should move cursor back to 0, got %d", m.cursor)
	}
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripAnsi(s string) string { return ansiRe.ReplaceAllString(s, "") }

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("abc", 5); got != "abc" {
		t.Fatalf("short string should be unchanged: %q", got)
	}
	if got := truncateRunes("abcde", 5); got != "abcde" {
		t.Fatalf("exact-length string should be unchanged: %q", got)
	}
	if got := truncateRunes("abcdef", 5); got != "abcd…" {
		t.Fatalf("over-length string should truncate with ellipsis: %q", got)
	}
}

func TestNewCollectingEnvCardModelStoresPrior(t *testing.T) {
	prior := []SummaryEntry{
		{Key: "A", Value: "1", Kind: EntryAdded},
		{Key: "B", Value: "2", Kind: EntryInherited},
	}
	m := newCollectingEnvCardModel(EnvEntry{}, prior)
	if len(m.prior) != 2 {
		t.Fatalf("prior length = %d, want 2", len(m.prior))
	}
	if m.prior[1].Kind != EntryInherited {
		t.Fatalf("prior[1].Kind = %v, want EntryInherited", m.prior[1].Kind)
	}
}

func TestTermWidthPositive(t *testing.T) {
	if w := termWidth(); w < 1 {
		t.Fatalf("termWidth = %d, want >= 1", w)
	}
}

func TestRenderSummaryEmpty(t *testing.T) {
	if got := renderSummary(defaultTheme(), nil, 80); got != "" {
		t.Fatalf("nil entries should render empty, got %q", got)
	}
}

func TestRenderSummaryKindsAndHeader(t *testing.T) {
	entries := []SummaryEntry{
		{Key: "PORT", Value: "5432", Kind: EntryAdded},
		{Key: "DATABASE_URL", Value: "postgres://staging", Kind: EntryOverride},
		{Key: "API_KEY", Value: "sk-a…(len=40)", Kind: EntryInherited},
		{Key: "LOG_LEVEL", Value: "info", Kind: EntryInherited},
	}
	got := stripAnsi(renderSummary(defaultTheme(), entries, 100))
	lines := strings.Split(got, "\n")
	if lines[0] != "Variables (2 own · 2 inherited)" {
		t.Fatalf("header = %q, want %q", lines[0], "Variables (2 own · 2 inherited)")
	}
	want := []string{
		"  + PORT = 5432",
		"  ↻ DATABASE_URL = postgres://staging",
		"  ↳ API_KEY = sk-a…(len=40)",
		"  ↳ LOG_LEVEL = info",
	}
	if len(lines) != 1+len(want) {
		t.Fatalf("got %d lines, want %d: %v", len(lines), 1+len(want), lines)
	}
	for i, w := range want {
		if lines[1+i] != w {
			t.Errorf("line %d = %q, want %q", 1+i, lines[1+i], w)
		}
	}
}

func TestRenderSummaryAddedHeaderWhenNoInherited(t *testing.T) {
	entries := []SummaryEntry{{Key: "PORT", Value: "5432", Kind: EntryAdded}}
	got := stripAnsi(renderSummary(defaultTheme(), entries, 80))
	if !strings.HasPrefix(got, "Added (1)\n  + PORT = 5432") {
		t.Fatalf("expected Added (1) header + line, got %q", got)
	}
}

func TestRenderSummaryTruncatesValue(t *testing.T) {
	entries := []SummaryEntry{{Key: "K", Value: "abcdefghijklmnop", Kind: EntryAdded}}
	got := stripAnsi(renderSummary(defaultTheme(), entries, 20))
	if !strings.Contains(got, "abcdefghijk…") {
		t.Fatalf("expected truncated value, got %q", got)
	}
}
