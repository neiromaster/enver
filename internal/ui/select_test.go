package ui

import (
	"fmt"
	"strings"
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

func TestSelectFilterConfirmSelectsFiltered(t *testing.T) {
	m := newSelectModel("t", []Option{
		{Value: "apple", Label: "Apple"},
		{Value: "banana", Label: "Banana"},
		{Value: "cherry", Label: "Cherry"},
	}, false)
	m = press(m, tea.KeyPressMsg{Text: "/"})          // enter filter
	m = press(m, tea.KeyPressMsg{Text: "ba"})         // narrows to Banana
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // exit filter mode (value retained)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // select
	if got := m.singleResult(); got != "banana" {
		t.Fatalf("filter-confirm = %q, want banana", got)
	}
}

func TestSelectCapturesHeight(t *testing.T) {
	m := newSelectModel("t", opts3(), false)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 42, Height: 7})
	sm := mm.(*selectModel)
	if sm.height != 7 {
		t.Fatalf("height = %d, want 7", sm.height)
	}
}

func TestSelectWindowScrollsToCursor(t *testing.T) {
	opts := make([]Option, 30)
	for i := range opts {
		opts[i] = Option{Value: fmt.Sprintf("v%d", i), Label: fmt.Sprintf("item-%d", i)}
	}
	m := newSelectModel("t", opts, false)
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 6}) // viewport = max(2, 10) = 10
	m.cursor = 20
	win := m.window()
	if len(win) != 10 {
		t.Fatalf("window len = %d, want 10", len(win))
	}
	seen := map[int]bool{}
	for _, i := range win {
		seen[i] = true
	}
	if !seen[20] {
		t.Error("cursor option 20 not in window")
	}
	if seen[0] || seen[29] {
		t.Error("far-away options should be scrolled out of the window")
	}
}

func TestSelectFilterBarShownAfterEnter(t *testing.T) {
	m := newSelectModel("t", []Option{
		{Value: "apple", Label: "Apple"},
		{Value: "banana", Label: "Banana"},
	}, false)
	m = press(m, tea.KeyPressMsg{Text: "/"})
	m = press(m, tea.KeyPressMsg{Text: "ba"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // exit filter mode, value retained
	if m.filter.active {
		t.Fatal("filter still active after enter")
	}
	if view := m.View().Content; !strings.Contains(view, "ba") {
		t.Fatalf("filter bar not shown after enter:\n%s", view)
	}
}

func TestMultiSelectActionNotToggleable(t *testing.T) {
	opts := []Option{
		{Value: "a", Label: "A"},
		{Value: "back", Label: "Back", Action: true},
	}
	m := newSelectModel("t", opts, true)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // cursor on Back
	m = press(m, tea.KeyPressMsg{Code: tea.KeySpace})
	if m.selected[1] {
		t.Fatal("action option was toggled by space")
	}
	m = press(m, tea.KeyPressMsg{Text: "*"}) // select-all skips action rows
	if m.selected[1] {
		t.Fatal("action option toggled by select-all")
	}
	if !m.selected[0] {
		t.Fatal("select-all did not toggle the non-action option")
	}
}

func TestMultiSelectEnterOnActionReturnsAction(t *testing.T) {
	opts := []Option{
		{Value: "a", Label: "A"},
		{Value: "back", Label: "Back", Action: true},
	}
	m := newSelectModel("t", opts, true)
	m = press(m, tea.KeyPressMsg{Code: tea.KeySpace}) // toggle A
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})  // cursor on Back
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.submitted {
		t.Fatal("not submitted")
	}
	got := m.multiResult()
	if len(got) != 1 || got[0] != "back" {
		t.Fatalf("multiResult = %v, want [back] (action cancels the checked set)", got)
	}
}

func TestSelectRendersColoredIcon(t *testing.T) {
	m := newSelectModel("t", []Option{
		{Value: "a", Label: "Add", Icon: IconAdd},
		{Value: "b", Label: "Other"},
	}, false)
	// cursor starts on row 0 (Add) — the active row.
	if view := m.View().Content; !strings.Contains(view, "32m") {
		t.Fatalf("active row missing colored (green) icon:\n%s", view)
	}
	// move cursor to Other; Add is now a non-active row — icon must stay colored.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if view := m.View().Content; !strings.Contains(view, "32m") {
		t.Fatalf("non-active row missing colored (green) icon:\n%s", view)
	}
}

func TestSelectActiveLabelCyan(t *testing.T) {
	m := newSelectModel("t", []Option{
		{Value: "a", Label: "Add", Icon: IconAdd},
	}, false)
	// cursor starts on row 0 (Add) — the active row.
	view := m.View().Content
	// The active label is highlighted bold+cyan (cyan = 36m). The title is also
	// cyan but its text differs, so "36mAdd" targets the active label.
	if !strings.Contains(view, "36mAdd") {
		t.Fatalf("active label not cyan-highlighted; expected cyan on \"Add\" in view:\n%s", view)
	}
}
