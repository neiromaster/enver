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

func TestSelectDefaultPositionsCursor(t *testing.T) {
	m := newSelectModel("t", opts3(), false) // a, b, c
	m.setDefault("c")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.singleResult(); got != "c" {
		t.Fatalf("def=c submitted %q, want c", got)
	}
}

func TestSelectDefaultEmptyStaysAtTop(t *testing.T) {
	m := newSelectModel("t", opts3(), false)
	m.setDefault("")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.singleResult(); got != "a" {
		t.Fatalf("empty def submitted %q, want a (top)", got)
	}
}

func TestSelectDefaultUnknownStaysAtTop(t *testing.T) {
	m := newSelectModel("t", opts3(), false)
	m.setDefault("zzz")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.singleResult(); got != "a" {
		t.Fatalf("unknown def submitted %q, want a (top)", got)
	}
}

func TestSelectDefaultSkipsSeparator(t *testing.T) {
	m := newSelectModel("t", []Option{
		{Value: "", Label: "(none)"},
		Separator(),
		{Value: "base", Label: "base"},
	}, false)
	m.setDefault("base")
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := m.singleResult(); got != "base" {
		t.Fatalf("def=base submitted %q, want base", got)
	}
}

func TestMultiSelectCheckedSeedsSelection(t *testing.T) {
	m := newCheckedMultiModel("t", opts3(), []string{"b"})
	if !m.selected[1] {
		t.Fatal("checked value b was not pre-selected")
	}
	if m.selected[0] || m.selected[2] {
		t.Fatalf("unchecked rows were pre-selected: %v", m.selected)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // cursor onto Beta
	m = press(m, tea.KeyPressMsg{Text: "x"})         // uncheck b again
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	got := m.multiResult()
	if len(got) != 0 {
		t.Fatalf("multiResult after unchecking = %v, want empty", got)
	}
}

func TestMultiSelectCheckedIgnoresUnknownValues(t *testing.T) {
	m := newCheckedMultiModel("t", opts3(), []string{"zzz"})
	for i := range opts3() {
		if m.selected[i] {
			t.Fatalf("unknown checked value selected option %d", i)
		}
	}
}

// checkedTestOptions is a checked-multi list with an action tail.
func checkedTestOptions() []Option {
	return []Option{
		{Value: "a", Label: "A"},
		{Value: "b", Label: "B"},
		{Value: "back", Label: "Back", Action: true},
	}
}

func TestMultiSelectCheckedEscAbortsButKeepsState(t *testing.T) {
	m := newCheckedMultiModel("t", checkedTestOptions(), []string{"a"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // cursor onto B
	m = press(m, tea.KeyPressMsg{Text: "x"})         // toggle B on
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if !m.aborted() {
		t.Fatal("esc should count as aborted")
	}
	got := m.checkedValues()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("checkedValues after esc = %v, want [a b]", got)
	}
}

func TestMultiSelectCheckedEnterOnActionAborts(t *testing.T) {
	m := newCheckedMultiModel("t", checkedTestOptions(), nil)
	m = press(m, tea.KeyPressMsg{Code: tea.KeySpace}) // toggle A
	m = press(m, tea.KeyPressMsg{Text: "G"})          // jump to the Back tail
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.aborted() {
		t.Fatal("enter on Back should count as aborted")
	}
	got := m.checkedValues()
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("checkedValues = %v, want [a] with no action value leaked", got)
	}
}

func TestMultiSelectCheckedConfirmReportsConfirmed(t *testing.T) {
	m := newCheckedMultiModel("t", checkedTestOptions(), []string{"a"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.aborted() {
		t.Fatal("plain confirm should not be aborted")
	}
	got := m.checkedValues()
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("confirmed values = %v, want [a]", got)
	}
}

func TestMultiSelectMinusKeyToggles(t *testing.T) {
	m := newSelectModel("t", opts3(), true)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // Beta
	m = press(m, tea.KeyPressMsg{Text: "-"})
	if !m.selected[1] {
		t.Fatal("'-' did not toggle the cursor option")
	}
	m = press(m, tea.KeyPressMsg{Text: "-"})
	if m.selected[1] {
		t.Fatal("'-' did not untoggle the cursor option")
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

func TestMultiSelectOrderedSeedsRankOrder(t *testing.T) {
	m := newOrderedMultiModel("t", opts3(), []string{"c", "a"})
	got := m.orderedValues()
	if len(got) != 2 || got[0] != "c" || got[1] != "a" {
		t.Fatalf("seed order = %v, want [c a] (seed sequence, not option order)", got)
	}
}

func TestMultiSelectOrderedSeedSkipsUnknownActionDuplicates(t *testing.T) {
	opts := []Option{
		{Value: "a", Label: "A"},
		{Value: "b", Label: "B"},
		{Value: "tail", Label: "Back", Action: true},
	}
	m := newOrderedMultiModel("t", opts, []string{"zzz", "tail", "b", "b", "a"})
	got := m.orderedValues()
	if len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("seed order = %v, want [b a] (unknown/action/duplicates skipped)", got)
	}
}

func TestMultiSelectOrderedToggleAppendsRank(t *testing.T) {
	m := newOrderedMultiModel("t", opts3(), []string{"c"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // cursor onto Beta
	m = press(m, tea.KeyPressMsg{Code: tea.KeySpace})
	got := m.orderedValues()
	if len(got) != 2 || got[0] != "c" || got[1] != "b" {
		t.Fatalf("order after toggle = %v, want [c b] (new picks append at the end)", got)
	}
}

func TestMultiSelectOrderedUntoggleShiftsRanks(t *testing.T) {
	m := newOrderedMultiModel("t", opts3(), []string{"a", "b", "c"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // cursor onto Beta (rank 2)
	m = press(m, tea.KeyPressMsg{Code: tea.KeySpace})
	got := m.orderedValues()
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("order after untoggle = %v, want [a c] (later ranks shift up)", got)
	}
}

func TestMultiSelectOrderedReorderKeysMoveRanks(t *testing.T) {
	m := newOrderedMultiModel("t", opts3(), []string{"a", "b", "c"})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // cursor onto Beta (rank 2)
	m = press(m, tea.KeyPressMsg{Text: "<"})
	got := m.orderedValues()
	if len(got) != 3 || got[0] != "b" || got[1] != "a" || got[2] != "c" {
		t.Fatalf("after < = %v, want [b a c]", got)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyRight})
	got = m.orderedValues()
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("after → = %v, want [a b c] (moved back in place)", got)
	}
}

func TestMultiSelectOrderedReorderBoundariesAndUnselected(t *testing.T) {
	m := newOrderedMultiModel("t", opts3(), []string{"a", "b"})
	m = press(m, tea.KeyPressMsg{Text: "<"}) // Alpha holds rank 1: no-op
	if got := m.orderedValues(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("< on the first rank changed order: %v", got)
	}
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown}) // cursor onto Gamma (no rank)
	m = press(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if got := m.orderedValues(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("reorder on an unselected row changed order: %v", got)
	}
}

func TestMultiSelectOrderedStarRanksInOptionOrder(t *testing.T) {
	m := newOrderedMultiModel("t", opts3(), []string{"b"})
	m = press(m, tea.KeyPressMsg{Text: "*"})
	got := m.orderedValues()
	if len(got) != 3 || got[0] != "b" || got[1] != "a" || got[2] != "c" {
		t.Fatalf("after * = %v, want [b a c] (missing picks append in option order)", got)
	}
	m = press(m, tea.KeyPressMsg{Text: "*"})
	if got := m.orderedValues(); len(got) != 0 {
		t.Fatalf("second * = %v, want empty (all-off clears ranks)", got)
	}
}

func TestMultiSelectOrderedRenderMarks(t *testing.T) {
	m := newOrderedMultiModel("t", opts3(), []string{"a"})
	view := m.View().Content
	if !strings.Contains(view, "\x1b[32m 1") {
		t.Fatalf("rank not rendered as a right-aligned green digit:\n%q", view)
	}
	if !strings.Contains(view, "\x1b[2m ·") {
		t.Fatalf("unselected rows not rendered as a faint dot:\n%q", view)
	}
	if strings.Contains(view, "●") {
		t.Fatal("ordered mode must not render the unordered ● mark")
	}
	if !strings.Contains(m.helpText(), "reorder") {
		t.Fatalf("help text missing reorder keys: %q", m.helpText())
	}
}

func TestMultiSelectOrderedRenderDoubleDigits(t *testing.T) {
	opts := make([]Option, 10)
	for i := range opts {
		opts[i] = Option{Value: fmt.Sprintf("v%d", i), Label: fmt.Sprintf("item-%d", i)}
	}
	seed := make([]string, 10)
	for i := range seed {
		seed[i] = fmt.Sprintf("v%d", i)
	}
	m := newOrderedMultiModel("t", opts, seed)
	if !strings.Contains(m.View().Content, "\x1b[32m10") {
		t.Fatalf("rank 10 not right-aligned in the two-cell column:\n%q", m.View().Content)
	}
}

func TestDimRowRendersFaint(t *testing.T) {
	m := newSelectModel("t", []Option{
		{Value: "a", Label: "Alpha"},
		{Value: "b", Label: "Beta", Dim: true},
	}, false)
	view := m.View().Content
	if !strings.Contains(view, "\x1b[2mBeta") {
		t.Fatalf("dim row not rendered faint:\n%s", view)
	}
	// The cursor row ignores Dim so the active highlight stays crisp.
	m = press(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if strings.Contains(m.View().Content, "\x1b[2mBeta") {
		t.Fatalf("cursor row should keep its active highlight, not dim:\n%s", m.View().Content)
	}
}
