package ui

import (
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
	theme     *theme
	cursor    int
	selected  map[int]bool
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

// MultiSelectChecked is MultiSelect with pre-checked rows (matched by Value).
// Confirming returns the still-checked values; selecting an Action row (e.g.
// Back) returns just that row's value like MultiSelect does.
func MultiSelectChecked(title string, options []Option, checked []string) ([]string, error) {
	out, err := run(newCheckedMultiModel(title, options, checked))
	if err != nil {
		return nil, err
	}
	return out.(*selectModel).multiResult(), nil
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

// setDefault moves the cursor onto the option whose Value matches def, so the
// default action (enter) keeps that option. An empty or unmatched def leaves the
// cursor at the top.
func (m *selectModel) setDefault(def string) {
	if def == "" {
		return
	}
	for pos, idx := range m.nav() {
		if m.options[idx].Value == def {
			m.cursor = pos
			return
		}
	}
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
		m.selected[idx] = !m.selected[idx]
	case k.Text == "*":
		var choices []int
		for _, i := range nav {
			if !m.options[i].Action {
				choices = append(choices, i)
			}
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

func (m *selectModel) Canceled() bool { return m.canceled }

func (m *selectModel) singleResult() string {
	if m.chosen >= 0 && m.chosen < len(m.options) {
		return m.options[m.chosen].Value
	}
	return ""
}

func (m *selectModel) multiResult() []string {
	if m.chosen >= 0 && m.chosen < len(m.options) && m.options[m.chosen].Action {
		return []string{m.options[m.chosen].Value}
	}
	var out []string
	for i, o := range m.options {
		if m.selected[i] {
			out = append(out, o.Value)
		}
	}
	return out
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
// regardless of glyph width.
func (m *selectModel) rowString(i, curOpt int) string {
	cur := " "
	if i == curOpt {
		cur = m.theme.cursor
	}
	cell := padIcon(m.theme.icon(m.options[i].Icon))
	label := m.options[i].Label
	if i == curOpt {
		label = m.theme.rowActive.Render(label)
	}
	if m.multi && !m.options[i].Action {
		mark := m.theme.checkOff
		if m.selected[i] {
			mark = m.theme.checkOn
		}
		return cur + " " + mark + " " + cell + " " + label
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
	if m.multi {
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

// SelectDefault is Select with the cursor initially on the option whose Value
// matches def (the top option when def is empty or unmatched). Use it to
// preserve a current value as the default accepted choice, e.g. re-running add
// on a profile that already extends another keeps that extends unless the user
// explicitly moves to (none).
func SelectDefault(title string, options []Option, def string) (string, error) {
	m := newSelectModel(title, options, false)
	m.setDefault(def)
	out, err := run(m)
	if err != nil {
		return "", err
	}
	return out.(*selectModel).singleResult(), nil
}

func MultiSelect(title string, options []Option) ([]string, error) {
	out, err := run(newSelectModel(title, options, true))
	if err != nil {
		return nil, err
	}
	return out.(*selectModel).multiResult(), nil
}
