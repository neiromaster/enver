package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type confirmModel struct {
	title     string
	value     bool
	theme     *theme
	submitted bool
	canceled  bool
}

func newConfirmModel(title string, defaultYes bool) *confirmModel {
	return &confirmModel{title: title, value: defaultYes, theme: defaultTheme()}
}

func (m *confirmModel) Init() tea.Cmd { return nil }

func (m *confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch {
	case k.Code == tea.KeyEsc, k.Mod == tea.ModCtrl && k.Code == 'c':
		m.canceled = true
		return m, tea.Quit
	case k.Code == tea.KeyEnter:
		m.submitted = true
		return m, tea.Quit
	case k.Text == "y":
		m.value = true
	case k.Text == "n":
		m.value = false
	case k.Code == tea.KeyLeft, k.Code == tea.KeyRight, k.Text == "h", k.Text == "l":
		m.value = !m.value
	}
	return m, nil
}

func (m *confirmModel) View() tea.View {
	var b strings.Builder
	b.WriteString(m.theme.title.Render(m.title))
	b.WriteString("\n")
	yes := "  Yes"
	no := "    No"
	if m.value {
		yes = m.theme.active.Render("▸ Yes")
	} else {
		no = m.theme.active.Render("▸   No")
	}
	b.WriteString(yes + "  " + no + "\n")
	b.WriteString(m.theme.help.Render("y/n · ←→ toggle · enter confirm · esc cancel"))
	return tea.NewView(b.String())
}

func (m *confirmModel) Canceled() bool { return m.canceled }

func Confirm(title string, defaultYes bool) (bool, error) {
	out, err := run(newConfirmModel(title, defaultYes))
	if err != nil {
		return false, err
	}
	m := out.(*confirmModel)
	return m.value, nil
}
