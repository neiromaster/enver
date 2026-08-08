package ui

import (
	"fmt"
	"strconv"
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
	labels := [3]string{"Name", "Value", "Comment"}
	blocks := make([]string, len(m.fields))
	for i := range m.fields {
		block := m.theme.title.Render(labels[i]) + "\n" + m.fields[i].View()
		if i == m.cursor {
			blocks[i] = m.theme.fieldActive.Render(block)
		} else {
			blocks[i] = m.theme.fieldIdle.Render(block)
		}
	}
	var b strings.Builder
	b.WriteString(strings.Join(blocks, "\n\n"))
	b.WriteString("\n")
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

// renderPriorEntries builds the "Added (N):" summary shown above a collecting
// env-card form. Output is unstyled (terminal-default weight) and ANSI-free.
// width is the usable terminal width; values are truncated with "…" to fit.
func renderPriorEntries(entries []EnvEntry, width int) string {
	if len(entries) == 0 {
		return ""
	}
	keyCol := 0
	for _, e := range entries {
		if len(e.Key) > keyCol {
			keyCol = len(e.Key)
		}
	}
	if keyCol > 20 {
		keyCol = 20
	}
	numW := len(strconv.Itoa(len(entries)))
	valueWidth := width - (2 + numW + 2) - keyCol - len(" = ")
	if valueWidth < 4 {
		valueWidth = 4
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Added (%d):\n", len(entries))
	for i, e := range entries {
		num := fmt.Sprintf("%*s", numW, strconv.Itoa(i+1))
		key := e.Key
		if len(key) < keyCol {
			key = fmt.Sprintf("%-*s", keyCol, key)
		}
		b.WriteString("  " + num + "  " + key + " = " + truncateRunes(e.Value, valueWidth))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// truncateRunes returns s unchanged when it fits in max runes; otherwise it
// returns the first max-1 runes followed by "…" (total max runes).
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

// EnvCard prompts for a single environment variable entry (key, value, comment).
// It returns (EnvEntry{Key:""}, nil) when the user finishes with a blank key name
// (callers check for empty Key to detect completion without error).
// Returns ErrCanceled if the user presses ESC or Ctrl+C.
func EnvCard(entry EnvEntry) (EnvEntry, error) {
	out, err := run(newEnvCardModel(entry))
	if err != nil {
		return EnvEntry{}, err
	}
	m := out.(*envCardModel)
	return m.result(), nil
}
