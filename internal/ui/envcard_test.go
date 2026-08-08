package ui

import (
	"regexp"
	"strconv"
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

func TestRenderPriorEntriesEmpty(t *testing.T) {
	if got := renderPriorEntries(nil, 80); got != "" {
		t.Fatalf("nil entries should render empty, got %q", got)
	}
	if got := renderPriorEntries([]EnvEntry{}, 80); got != "" {
		t.Fatalf("empty entries should render empty, got %q", got)
	}
}

func TestRenderPriorEntriesHeaderAndAlignment(t *testing.T) {
	entries := []EnvEntry{
		{Key: "DATABASE_URL", Value: "postgres://localhost/db"},
		{Key: "PORT", Value: "5432"},
	}
	got := stripAnsi(renderPriorEntries(entries, 100))
	lines := strings.Split(got, "\n")

	if lines[0] != "Added (2):" {
		t.Fatalf("header = %q, want %q", lines[0], "Added (2):")
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2), got %d: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[1], "  1  DATABASE_URL = ") {
		t.Fatalf("first entry line = %q", lines[1])
	}
	// Every entry line must have its "=" at the same column (number right-align + key padding).
	cols := map[int]bool{}
	for _, ln := range lines[1:] {
		cols[strings.Index(ln, "=")] = true
	}
	if len(cols) != 1 {
		t.Fatalf("= columns not aligned: %v", cols)
	}
}

func TestRenderPriorEntriesMultiDigitNumbers(t *testing.T) {
	entries := make([]EnvEntry, 10)
	for i := range entries {
		entries[i] = EnvEntry{Key: "K" + strconv.Itoa(i), Value: "v"}
	}
	got := stripAnsi(renderPriorEntries(entries, 100))
	lines := strings.Split(got, "\n")
	if !strings.HasPrefix(lines[1], "   1  ") { // right-aligned within a 2-wide number field
		t.Fatalf("first numbered line = %q, want leading \"   1  \"", lines[1])
	}
	if !strings.HasPrefix(lines[10], "  10  ") {
		t.Fatalf("tenth numbered line = %q, want leading \"  10  \"", lines[10])
	}
}

func TestRenderPriorEntriesTruncatesLongValue(t *testing.T) {
	entries := []EnvEntry{{Key: "K", Value: "abcdefghijklmnop"}} // 16 chars
	got := stripAnsi(renderPriorEntries(entries, 20))
	// width 20: prefix "  1  " (5) + key "K" (1) + " = " (3) = 9 used; value budget = 11 -> 10 runes + "…"
	if !strings.Contains(got, "abcdefghij…") {
		t.Fatalf("expected truncated value, got %q", got)
	}
	if strings.Contains(got, "abcdefghijk") {
		t.Fatalf("value should be truncated before the 11th char, got %q", got)
	}
}

func TestRenderPriorEntriesEmptyValue(t *testing.T) {
	entries := []EnvEntry{{Key: "EMPTY", Value: ""}}
	got := stripAnsi(renderPriorEntries(entries, 100))
	lines := strings.Split(got, "\n")
	if strings.TrimRight(lines[1], " ") != "  1  EMPTY =" {
		t.Fatalf("empty-value line = %q", lines[1])
	}
}

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
