package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type envCardModel struct {
	fields    [3]textinput.Model
	cursor    int
	theme     *theme
	submitted bool
	canceled  bool
}

func newEnvCardModel(e EnvEntry) *envCardModel {
	m := &envCardModel{theme: defaultTheme()}
	seeds := []string{e.Key, e.Value, e.Comment}
	for i := 0; i < 3; i++ {
		ti := textinput.New()
		ti.Prompt = ""
		ti.SetValue(seeds[i])
		m.fields[i] = ti
	}
	m.fields[0].Focus()
	return m
}

func (m *envCardModel) Init() tea.Cmd { return textinput.Blink }

func (m *envCardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case k.Code == tea.KeyEsc, k.Mod == tea.ModCtrl && k.Code == 'c':
			m.canceled = true
			return m, tea.Quit
		case k.Code == tea.KeyEnter:
			if m.cursor == 0 && m.fields[0].Value() == "" {
				return m, tea.Quit
			}
			if m.cursor == len(m.fields)-1 {
				m.submitted = true
				return m, tea.Quit
			}
			m.advance(1)
			return m, nil
		case k.Code == tea.KeyTab && k.Mod == tea.ModShift:
			m.advance(-1)
			return m, nil
		case k.Code == tea.KeyTab:
			m.advance(1)
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.fields[m.cursor], cmd = m.fields[m.cursor].Update(msg)
	return m, cmd
}

func (m *envCardModel) advance(delta int) {
	m.fields[m.cursor].Blur()
	m.cursor = clamp(m.cursor+delta, 0, len(m.fields)-1)
	m.fields[m.cursor].Focus()
}

func (m *envCardModel) View() tea.View {
	var b strings.Builder
	labels := [3]string{"Name", "Value", "Comment"}
	for i := range m.fields {
		prefix := "  "
		if i == m.cursor {
			prefix = m.theme.cursor + " "
		}
		b.WriteString(m.theme.title.Render(prefix + labels[i]))
		b.WriteString("\n")
		b.WriteString(m.fields[i].View())
		b.WriteString("\n")
	}
	b.WriteString(m.theme.help.Render("tab next · shift+tab prev · enter submit · blank name finishes · esc cancel"))
	return tea.NewView(b.String())
}

func (m *envCardModel) Canceled() bool { return m.canceled }

func (m *envCardModel) result() EnvEntry {
	return EnvEntry{
		Key:     m.fields[0].Value(),
		Value:   m.fields[1].Value(),
		Comment: m.fields[2].Value(),
	}
}

func EnvCard(entry EnvEntry) (EnvEntry, error) {
	out, err := run(newEnvCardModel(entry))
	if err != nil {
		return EnvEntry{}, err
	}
	m := out.(*envCardModel)
	return m.result(), nil
}
