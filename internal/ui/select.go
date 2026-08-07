package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
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
	}
}

func (m *selectModel) Init() tea.Cmd { return nil }

func (m *selectModel) nav() []int {
	var out []int
	for i, o := range m.options {
		if o.Separator {
			continue
		}
		if m.filter.active && m.filter.value != "" &&
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
			if !m.multi {
				if nav := m.nav(); len(nav) > 0 {
					m.chosen = nav[m.cursor]
				}
			}
			return m, tea.Quit
		case k.Code == tea.KeyDown, k.Text == "j":
			m.move(1)
		case k.Code == tea.KeyUp, k.Text == "k":
			m.move(-1)
		case k.Text == "g", k.Code == tea.KeyHome:
			m.cursor = 0
		case k.Text == "G", k.Code == tea.KeyEnd:
			m.cursor = len(m.nav()) - 1
		case k.Code == tea.KeyPgDown, k.Code == tea.KeyPgUp:
			m.page(k.Code)
		}
		if k.Mod == tea.ModCtrl && (k.Code == 'u' || k.Code == 'd') {
			step := m.pageHeight() / 2
			if step < 1 {
				step = 1
			}
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
	switch {
	case k.Code == tea.KeyEsc:
		m.filter.active = false
		m.filter.value = ""
	case k.Code == tea.KeyEnter:
		m.filter.active = false
	case k.Code == tea.KeyBackspace:
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
	step := m.pageHeight()
	if step < 1 {
		step = 1
	}
	if code == tea.KeyPgDown {
		m.move(step)
	} else {
		m.move(-step)
	}
}

func (m *selectModel) pageHeight() int {
	if m.height <= 0 {
		return len(m.nav())
	}
	return m.height - 4
}

func (m *selectModel) applyMultiKey(k tea.KeyPressMsg) {
	nav := m.nav()
	if len(nav) == 0 {
		return
	}
	switch {
	case k.Code == tea.KeySpace, k.Text == "x":
		idx := nav[m.cursor]
		m.selected[idx] = !m.selected[idx]
	case k.Text == "*":
		allOn := true
		for _, i := range nav {
			if !m.selected[i] {
				allOn = false
				break
			}
		}
		for _, i := range nav {
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

func (m *selectModel) View() tea.View {
	var b strings.Builder
	b.WriteString(m.theme.title.Render(m.title))
	b.WriteString("\n")
	visible := m.visibleIndices()
	current := -1
	if nav := m.nav(); len(nav) > 0 {
		current = nav[m.cursor]
	}
	for _, i := range visible {
		o := m.options[i]
		if o.Separator {
			b.WriteString(m.theme.separator.Render(strings.Repeat("─", 24)))
			b.WriteString("\n")
			continue
		}
		cursor := " "
		if i == current {
			cursor = m.theme.cursor
		}
		line := o.Label
		if m.multi {
			mark := m.theme.checkOff
			if m.selected[i] {
				mark = m.theme.checkOn
			}
			line = mark + " " + line
		}
		if i == current {
			line = m.theme.selected.Render(cursor + " " + line)
		} else {
			line = m.theme.normal.Render(cursor + " " + line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if m.filter.active {
		b.WriteString(m.theme.filter.Render("/" + m.filter.value))
		b.WriteString("\n")
	}
	b.WriteString(m.theme.help.Render(m.helpText()))
	return tea.NewView(b.String())
}

func (m *selectModel) visibleIndices() []int {
	if m.filter.active && m.filter.value != "" {
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
		return "↑↓ move · space toggle · * all · / filter · enter confirm · esc cancel"
	}
	return "↑↓ move · / filter · enter select · esc cancel"
}

func Select(title string, options []Option) (string, error) {
	out, err := run(newSelectModel(title, options, false))
	if err != nil {
		return "", err
	}
	return out.(*selectModel).singleResult(), nil
}
