package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func opts3() []Option {
	return []Option{
		{Value: "a", Label: "Alpha"},
		{Value: "b", Label: "Beta"},
		{Value: "c", Label: "Gamma"},
	}
}

func press(m *selectModel, k tea.KeyPressMsg) *selectModel {
	mm, _ := m.Update(k)
	return mm.(*selectModel)
}

func TestSelectNavSkipsSeparators(t *testing.T) {
	m := newSelectModel("t", []Option{
		{Value: "a", Label: "A"},
		Separator(),
		{Value: "b", Label: "B"},
	}, false)
	if got := m.nav()[m.cursor]; got != 0 {
		t.Fatalf("start cursor option = %d, want 0", got)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if got := m.nav()[m.cursor]; got != 2 {
		t.Fatalf("after down, cursor option = %d, want 2 (separator at 1 skipped)", got)
	}
}

func TestSelectFilterNarrows(t *testing.T) {
	m := newSelectModel("t", opts3(), false)
	m = press(m, tea.KeyPressMsg{Text: "/"}) // enter filter mode
	m = press(m, tea.KeyPressMsg{Text: "l"}) // match "Alpha" only
	if len(m.nav()) != 1 {
		t.Fatalf("filter 'l' nav len = %d, want 1 (Alpha)", len(m.nav()))
	}
}

func TestSelectSubmitReturnsValue(t *testing.T) {
	m := newSelectModel("t", opts3(), false)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // cursor on Beta
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.submitted {
		t.Fatal("not submitted")
	}
	if got := m.singleResult(); got != "b" {
		t.Fatalf("singleResult = %q, want b", got)
	}
}

func TestSelectCancel(t *testing.T) {
	m := newSelectModel("t", opts3(), false)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if !m.canceled {
		t.Fatal("esc did not set canceled")
	}
	m2 := newSelectModel("t", opts3(), false)
	m2 = press(m2, tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'c'})
	if !m2.canceled {
		t.Fatal("ctrl+c did not set canceled")
	}
}

func TestSelectNavClamps(t *testing.T) {
	m := newSelectModel("t", opts3(), false)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyUp}) // already at top
	if m.cursor != 0 {
		t.Fatalf("up at top moved cursor to %d, want 0", m.cursor)
	}
	m = press(m, tea.KeyPressMsg{Text: "G"}) // end
	if m.cursor != len(m.nav())-1 {
		t.Fatalf("G cursor = %d, want %d", m.cursor, len(m.nav())-1)
	}
}

func TestMultiSelectToggleAndSubmit(t *testing.T) {
	m := newSelectModel("t", opts3(), true)
	m = press(m, tea.KeyPressMsg{Code: tea.KeySpace}) // toggle Alpha
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})  // Beta
	m = press(m, tea.KeyPressMsg{Text: "x"})          // toggle Beta
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.submitted {
		t.Fatal("not submitted")
	}
	got := m.multiResult()
	want := map[string]bool{"a": true, "b": true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Fatalf("multiResult = %v, want a,b", got)
	}
}

func TestMultiSelectStarTogglesAll(t *testing.T) {
	m := newSelectModel("t", opts3(), true)
	m = press(m, tea.KeyPressMsg{Text: "*"}) // all on
	for _, i := range m.nav() {
		if !m.selected[i] {
			t.Fatalf("option %d not selected after *", i)
		}
	}
	m = press(m, tea.KeyPressMsg{Text: "*"}) // all off
	for _, i := range m.nav() {
		if m.selected[i] {
			t.Fatalf("option %d still selected after second *", i)
		}
	}
}
