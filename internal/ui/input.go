package ui

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
)

type inputModel struct {
	title     string
	ti        textinput.Model
	theme     *theme
	submitted bool
	canceled  bool
}

func newInputModel(title string) *inputModel {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Focus()
	return &inputModel{title: title, ti: ti, theme: defaultTheme()}
}

func (m *inputModel) Init() tea.Cmd { return textinput.Blink }

func (m *inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if ok {
		switch {
		case k.Code == tea.KeyEsc, k.Mod == tea.ModCtrl && k.Code == 'c':
			m.canceled = true
			return m, tea.Quit
		case k.Code == tea.KeyEnter:
			m.submitted = true
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	return m, cmd
}

func (m *inputModel) View() tea.View {
	var b strings.Builder
	b.WriteString(m.theme.title.Render(m.title))
	b.WriteString("\n")
	b.WriteString(m.ti.View())
	b.WriteString("\n")
	b.WriteString(m.theme.help.Render("enter submit · esc cancel"))
	return tea.NewView(b.String())
}

func (m *inputModel) Canceled() bool { return m.canceled }

func (m *inputModel) result() string { return m.ti.Value() }

func Input(title string) (string, error) {
	out, err := run(newInputModel(title))
	if err != nil {
		return "", err
	}
	return out.(*inputModel).result(), nil
}

// Password reads a hidden passphrase from the terminal. The prompt goes to
// stderr so it never pollutes piped stdout. Returns ErrCanceled on empty input.
func Password(prompt string) (string, error) {
	if !Interactive() {
		return "", errors.New("interactive prompt requires a terminal")
	}
	fmt.Fprint(os.Stderr, prompt+" ")
	pass, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if len(pass) == 0 {
		return "", ErrCanceled
	}
	return string(pass), nil
}
