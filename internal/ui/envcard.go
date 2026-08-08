package ui

import (
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
)

type envCardModel struct {
	fields    [3]textinput.Model
	cursor    int
	theme     *theme
	submitted bool
	canceled  bool
	prior     []SummaryEntry
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

// termWidth returns the usable terminal width, falling back to 80 when the
// width cannot be determined (e.g. non-terminal stdout).
func termWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}

func newCollectingEnvCardModel(e EnvEntry, prior []SummaryEntry) *envCardModel {
	m := newEnvCardModel(e)
	m.prior = prior
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
	if len(m.prior) > 0 {
		b.WriteString(renderSummary(m.theme, m.prior, termWidth()))
		b.WriteString("\n\n")
	}
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

// EntryKind classifies a summary entry so the renderer can pick its icon.
type EntryKind int

const (
	EntryAdded     EntryKind = iota // "+": own, just added
	EntryOverride                   // "↻": own, shadows an inherited variable
	EntryInherited                  // "↳": contributed by the extends chain
)

// SummaryEntry is one line of the collecting env-card summary: a masked key/value
// plus the semantic kind that selects its icon.
type SummaryEntry struct {
	Key   string
	Value string // already masked by the caller
	Kind  EntryKind
}

// renderSummary builds the icon-prefixed summary shown above a collecting env-card
// form: a count header, then one indented line per entry in the given order. Icons
// are colored via the theme; values are truncated with "…" to the terminal width.
func renderSummary(t *theme, entries []SummaryEntry, width int) string {
	if len(entries) == 0 {
		return ""
	}
	var own, inh int
	for _, e := range entries {
		if e.Kind == EntryInherited {
			inh++
		} else {
			own++
		}
	}
	glyph := map[EntryKind]string{EntryAdded: IconAdd, EntryOverride: IconOverride, EntryInherited: IconInherited}

	var b strings.Builder
	if inh > 0 {
		fmt.Fprintf(&b, "Variables (%d own · %d inherited)\n", own, inh)
	} else {
		fmt.Fprintf(&b, "Added (%d)\n", own)
	}
	for _, e := range entries {
		budget := width - len("  ") - 1 - len(" ") - len(e.Key) - len(" = ")
		if budget < 4 {
			budget = 4
		}
		b.WriteString("  " + t.icon(glyph[e.Kind]) + " " + e.Key + " = " + truncateRunes(e.Value, budget))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
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

// EnvCardCollecting prompts for one variable in a multi-variable collection flow
// (used by the add command). It renders an icon-prefixed summary of the already
// collected variables above the form. summary is display-only and should already
// be masked by the caller.
func EnvCardCollecting(entry EnvEntry, summary []SummaryEntry) (EnvEntry, error) {
	out, err := run(newCollectingEnvCardModel(entry, summary))
	if err != nil {
		return EnvEntry{}, err
	}
	return out.(*envCardModel).result(), nil
}
