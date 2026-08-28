package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type filterState struct {
	active bool
	value  string
}

type selectModel struct {
	title     string
	options   []Option
	multi     bool
	ordered   bool
	theme     *theme
	cursor    int
	selected  map[int]bool
	order     []int
	filter    filterState
	height    int
	submitted bool
	canceled  bool
	chosen    int
}

func newSelectModel(title string, options []Option, multi bool) *selectModel {
	return &selectModel{
		title:    title,
		options:  options,
		multi:    multi,
		theme:    defaultTheme(),
		selected: map[int]bool{},
		chosen:   -1,
	}
}

// newOrderedMultiModel is the MultiSelectOrdered engine: the multi model with
// ranks seeded from order. Values are ranked in the sequence given; unknown
// values, action rows, and duplicates never seed a rank.
func newOrderedMultiModel(title string, options []Option, order []string) *selectModel {
	m := newSelectModel(title, options, true)
	m.ordered = true
	for _, v := range order {
		for i, o := range options {
			if o.Action || o.Value != v {
				continue
			}
			if _, seen := m.rankOf(i); !seen {
				m.order = append(m.order, i)
			}
			break
		}
	}
	return m
}

// rankOf reports the position of option i in the selection order.
func (m *selectModel) rankOf(i int) (int, bool) {
	for r, idx := range m.order {
		if idx == i {
			return r, true
		}
	}
	return 0, false
}

// shiftRank moves the cursor option one position earlier (<, ←) or later
// (>, →) in the selection order. Rows without a rank and the boundary ranks
// are no-ops, as are the keys outside ordered mode.
func (m *selectModel) shiftRank(delta int) {
	if !m.ordered {
		return
	}
	nav := m.nav()
	if len(nav) == 0 {
		return
	}
	r, on := m.rankOf(nav[m.cursor])
	if !on {
		return
	}
	t := r + delta
	if t < 0 || t >= len(m.order) {
		return
	}
	m.order[r], m.order[t] = m.order[t], m.order[r]
}

// orderedValues returns the selected Values in rank order. Only non-Action
// rows can enter the order (seeding, toggleRanked, and toggleAllRanked all
// refuse them), so no per-row re-check is needed here.
func (m *selectModel) orderedValues() []string {
	out := make([]string, 0, len(m.order))
	for _, i := range m.order {
		out = append(out, m.options[i].Value)
	}
	return out
}

// newCheckedMultiModel is the MultiSelectChecked engine: the same multi model
// with options whose Value appears in checked pre-marked. Unknown values and
// action rows never seed a mark.
func newCheckedMultiModel(title string, options []Option, checked []string) *selectModel {
	m := newSelectModel(title, options, true)
	if len(checked) == 0 {
		return m
	}
	on := make(map[string]bool, len(checked))
	for _, v := range checked {
		on[v] = true
	}
	for i, o := range options {
		if !o.Action && on[o.Value] {
			m.selected[i] = true
		}
	}
	return m
}

// MultiSelectChecked is a checkbox multi-select with pre-checked rows (matched
// by Value). Confirming reports (values, true, nil) where values are the
// still-checked Values in option order. Leaving without confirming — esc/ctrl+c
// or enter on an Action row such as Back — reports (state, false, nil), state
// being the checkbox layout at that moment, so callers can tell a bare exit
// from a session that had toggles pending. Action rows never appear in values.
func MultiSelectChecked(title string, options []Option, checked []string) ([]string, bool, error) {
	out, err := run(newCheckedMultiModel(title, options, checked))
	if err != nil {
		return nil, false, err
	}
	m := out.(*selectModel)
	return m.checkedValues(), !m.aborted(), nil
}

// MultiSelectOrdered is a multi-select that preserves selection order: ranks
// seed from order (matched by Value, in the sequence given), picking appends
// to the end, </>/←→ move rows within the order. Confirming reports the
// selected Values in rank order; leaving without confirming — esc/ctrl+c or
// enter on an Action row such as Back — reports the current order and false,
// so callers can tell a bare exit from a session with pending changes.
func MultiSelectOrdered(title string, options []Option, order []string) ([]string, bool, error) {
	out, err := run(newOrderedMultiModel(title, options, order))
	if err != nil {
		return nil, false, err
	}
	m := out.(*selectModel)
	return m.orderedValues(), !m.aborted(), nil
}

// checkedValues returns the checked non-Action Values in option order — both
// when the session was confirmed and when it was abandoned mid-edit.
func (m *selectModel) checkedValues() []string {
	values := make([]string, 0, len(m.selected))
	for i, o := range m.options {
		if o.Action || !m.selected[i] {
			continue
		}
		values = append(values, o.Value)
	}
	return values
}

func (m *selectModel) Init() tea.Cmd { return nil }

func (m *selectModel) nav() []int {
	var out []int
	for i, o := range m.options {
		if o.Separator {
			continue
		}
		if m.filter.value != "" &&
			!strings.Contains(strings.ToLower(o.Label), strings.ToLower(m.filter.value)) {
			continue
		}
		out = append(out, i)
	}
	return out
}

func (m *selectModel) move(delta int) {
	nav := m.nav()
	if len(nav) == 0 {
		m.cursor = 0
		return
	}
	m.cursor = clamp(m.cursor+delta, 0, len(nav)-1)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (m *selectModel) isCancel(k tea.KeyPressMsg) bool {
	return k.Code == tea.KeyEsc || (k.Mod == tea.ModCtrl && k.Code == 'c')
}

func (m *selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch k := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = k.Height
		return m, nil
	case tea.KeyPressMsg:
		if m.filter.active {
			return m.applyFilterKey(k)
		}
		switch {
		case m.isCancel(k):
			m.canceled = true
			return m, tea.Quit
		case k.Code == tea.KeyEnter:
			m.submitted = true
			if nav := m.nav(); len(nav) > 0 {
				m.chosen = nav[m.cursor]
			}
			return m, tea.Quit
		case k.Code == tea.KeyDown, k.Text == "j":
			m.move(1)
		case k.Code == tea.KeyUp, k.Text == "k":
			m.move(-1)
		case k.Text == "g", k.Code == tea.KeyHome:
			m.cursor = 0
		case k.Text == "G", k.Code == tea.KeyEnd:
			m.cursor = max(0, len(m.nav())-1)
		case k.Code == tea.KeyPgDown, k.Code == tea.KeyPgUp:
			m.page(k.Code)
		case k.Code == tea.KeyLeft, k.Text == "<":
			m.shiftRank(-1)
		case k.Code == tea.KeyRight, k.Text == ">":
			m.shiftRank(+1)
		}
		if k.Mod == tea.ModCtrl && (k.Code == 'u' || k.Code == 'd') {
			step := max(m.viewport()/2, 1)
			if k.Code == 'u' {
				m.move(-step)
			} else {
				m.move(step)
			}
		}
		if k.Text == "/" {
			m.filter.active = true
			m.filter.value = ""
			m.cursor = 0
		}
		if m.multi {
			m.applyMultiKey(k)
		}
	}
	return m, nil
}

func (m *selectModel) applyFilterKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.Code {
	case tea.KeyEsc:
		m.filter.active = false
		m.filter.value = ""
	case tea.KeyEnter:
		m.filter.active = false
	case tea.KeyBackspace:
		if len(m.filter.value) > 0 {
			m.filter.value = m.filter.value[:len(m.filter.value)-1]
		}
	default:
		if k.Text != "" {
			m.filter.value += k.Text
		}
	}
	m.cursor = clamp(m.cursor, 0, max(0, len(m.nav())-1))
	return m, nil
}

func (m *selectModel) page(code rune) {
	step := m.viewport()
	if code == tea.KeyPgDown {
		m.move(step)
	} else {
		m.move(-step)
	}
}

// viewport is the number of list rows to render and to page by. It follows the
// terminal height but never collapses below floor, so navigation stays usable on
// small terminals.
func (m *selectModel) viewport() int {
	const floor = 10
	if m.height <= 0 {
		return floor
	}
	return max(m.height-4, floor)
}

// window returns the displayed option indices to render, centered on the cursor
// and capped at viewport() rows so long lists scroll instead of overflowing.
func (m *selectModel) window() []int {
	disp := m.visibleIndices()
	rows := m.viewport()
	if len(disp) <= rows {
		return disp
	}
	curOpt := -1
	if nav := m.nav(); len(nav) > 0 {
		curOpt = nav[m.cursor]
	}
	start := clamp(indexIn(disp, curOpt)-rows/2, 0, len(disp)-rows)
	return disp[start : start+rows]
}

func (m *selectModel) applyMultiKey(k tea.KeyPressMsg) {
	nav := m.nav()
	if len(nav) == 0 {
		return
	}
	switch {
	case k.Code == tea.KeySpace, k.Text == "x", k.Text == "-":
		idx := nav[m.cursor]
		if m.options[idx].Action {
			return
		}
		if m.ordered {
			m.toggleRanked(idx)
		} else {
			m.selected[idx] = !m.selected[idx]
		}
	case k.Text == "*":
		var choices []int
		for _, i := range nav {
			if !m.options[i].Action {
				choices = append(choices, i)
			}
		}
		if m.ordered {
			m.toggleAllRanked(choices)
			return
		}
		allOn := true
		for _, i := range choices {
			if !m.selected[i] {
				allOn = false
				break
			}
		}
		for _, i := range choices {
			m.selected[i] = !allOn
		}
	}
}

// toggleRanked flips option idx in ordered mode: picking appends to the end
// of the order, dropping removes the rank and shifts later ones up. The rank
// order is the single source of truth — the selected set stays untouched.
func (m *selectModel) toggleRanked(idx int) {
	if r, on := m.rankOf(idx); on {
		m.order = append(m.order[:r], m.order[r+1:]...)
		return
	}
	m.order = append(m.order, idx)
}

// toggleAllRanked applies * in ordered mode: when every choice is ranked it
// clears the whole order, otherwise it ranks the missing choices in option
// order.
func (m *selectModel) toggleAllRanked(choices []int) {
	allOn := len(choices) > 0
	for _, i := range choices {
		if _, on := m.rankOf(i); !on {
			allOn = false
			break
		}
	}
	if allOn {
		m.order = nil
		return
	}
	for _, i := range choices {
		if _, on := m.rankOf(i); !on {
			m.order = append(m.order, i)
		}
	}
}

func (m *selectModel) Canceled() bool { return m.canceled }

// aborted reports whether the session ended without a confirm: esc/ctrl+c, or
// enter on an Action row such as the Back tail.
func (m *selectModel) aborted() bool {
	if m.canceled {
		return true
	}
	return m.chosen >= 0 && m.chosen < len(m.options) && m.options[m.chosen].Action
}

func (m *selectModel) singleResult() string {
	if m.chosen >= 0 && m.chosen < len(m.options) {
		return m.options[m.chosen].Value
	}
	return ""
}

func (m *selectModel) View() tea.View {
	var b strings.Builder
	b.WriteString(m.theme.title.Render(m.title))
	b.WriteString("\n")

	curOpt := -1
	if nav := m.nav(); len(nav) > 0 {
		curOpt = nav[m.cursor]
	}

	for _, i := range m.window() {
		o := m.options[i]
		if o.Separator {
			b.WriteString(m.theme.separator.Render(strings.Repeat("─", 24)))
			b.WriteString("\n")
			continue
		}
		line := m.rowString(i, curOpt)
		line = m.theme.normal.Render(line)
		b.WriteString(line)
		b.WriteString("\n")
	}

	if m.filter.value != "" {
		if m.filter.active {
			b.WriteString(m.theme.filter.Render("/" + m.filter.value))
		} else {
			b.WriteString(m.theme.help.Render("filter: " + m.filter.value))
		}
		b.WriteString("\n")
	}
	b.WriteString(m.theme.help.Render(m.helpText()))
	return tea.NewView(b.String())
}

// rowString builds the (unstyled) text of one option row: cursor, an optional
// multi-select mark, and a fixed-width icon cell so labels align across rows
// regardless of glyph width. In ordered mode the mark is the rank: a
// right-aligned two-cell digit for ranked rows, a faint dot for the rest.
// A Dim option fades every cell — except on the cursor row, where the active
// highlight must stay crisp.
func (m *selectModel) rowString(i, curOpt int) string {
	cur := " "
	if i == curOpt {
		cur = m.theme.cursor
	}
	style := m.theme.normal
	if m.options[i].Dim && i != curOpt {
		style = m.theme.dim
	}
	cell := style.Render(padIcon(m.theme.icon(m.options[i].Icon)))
	label := m.options[i].Label
	switch {
	case i == curOpt:
		label = m.theme.rowActive.Render(label)
	case m.options[i].Dim:
		label = m.theme.dim.Render(label)
	}
	if m.multi && !m.options[i].Action {
		mark := m.theme.checkOff
		if m.ordered {
			if r, on := m.rankOf(i); on {
				mark = m.theme.rank.Render(fmt.Sprintf("%2d", r+1))
			} else {
				mark = m.theme.dim.Render(" ·")
			}
		} else if m.selected[i] {
			mark = m.theme.checkOn
		}
		return cur + " " + style.Render(mark) + " " + cell + " " + label
	}
	return cur + " " + cell + " " + label
}

func (m *selectModel) visibleIndices() []int {
	if m.filter.value != "" {
		return m.nav()
	}
	out := make([]int, len(m.options))
	for i := range m.options {
		out[i] = i
	}
	return out
}

func (m *selectModel) helpText() string {
	switch {
	case m.ordered:
		return "↑↓ move · space toggle · </>/←→ reorder · * all · / filter · enter confirm · esc cancel"
	case m.multi:
		return "↑↓ move · space/- toggle · * all · / filter · enter confirm · esc cancel"
	}
	return "↑↓ move · / filter · enter select · esc cancel"
}

func padIcon(icon string) string {
	pad := max(0, 2-lipgloss.Width(icon))
	return icon + strings.Repeat(" ", pad)
}

func indexIn(xs []int, v int) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return 0
}

func Select(title string, options []Option) (string, error) {
	out, err := run(newSelectModel(title, options, false))
	if err != nil {
		return "", err
	}
	return out.(*selectModel).singleResult(), nil
}
