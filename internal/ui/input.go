package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

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

// hiddenLine assembles one hidden input line from raw terminal bytes:
// printable bytes append, backspace and Ctrl-H erase the last rune, Ctrl-W
// erases the previous word, Ctrl-U clears, Ctrl-C (arriving as data — ISIG is
// off) and Ctrl-D on an empty line cancel, Ctrl-V quotes the next byte so it
// arrives as data, Enter ends the line. Editing and flow controls the line
// discipline used to consume (Ctrl-R, Ctrl-S, Ctrl-Q, Ctrl-O) are swallowed,
// not appended — raw mode turned off the discipline, not the habits.
type hiddenLine struct {
	buf      []byte
	done     bool
	canceled bool
	lnext    bool // Ctrl-V quoted the next byte: it arrives as data
}

// feed applies one raw byte and reports whether the line is finished.
func (h *hiddenLine) feed(b byte) bool {
	if h.lnext {
		h.lnext = false
		h.buf = append(h.buf, b)
		return h.done
	}
	switch b {
	case 3:
		h.canceled, h.done = true, true
	case '\r', '\n':
		h.done = true
	case 0x7f, 8:
		if _, sz := utf8.DecodeLastRune(h.buf); sz > 0 {
			h.buf = h.buf[:len(h.buf)-sz]
		}
	case 21:
		h.buf = h.buf[:0]
	case 0x17:
		h.eraseWord()
	case 0x16:
		h.lnext = true
	case 4:
		if len(h.buf) == 0 {
			h.canceled, h.done = true, true
		}
	case 0x12, 0x11, 0x13, 0x0f:
		// REPRINT, XON, XOFF, DISCARD: discipline-level controls, never input.
	default:
		h.buf = append(h.buf, b)
	}
	return h.done
}

// eraseWord implements WERASE: trailing whitespace goes first, then the run of
// non-whitespace before it — rune-wise, so multibyte input survives intact.
func (h *hiddenLine) eraseWord() {
	for len(h.buf) > 0 {
		r, sz := utf8.DecodeLastRune(h.buf)
		if !unicode.IsSpace(r) {
			break
		}
		h.buf = h.buf[:len(h.buf)-sz]
	}
	for len(h.buf) > 0 {
		r, sz := utf8.DecodeLastRune(h.buf)
		if unicode.IsSpace(r) {
			break
		}
		h.buf = h.buf[:len(h.buf)-sz]
	}
}

// readHiddenFrom drains one edited line from r. An EOF flushes whatever was
// typed; a cancel or an EOF at an empty line yields ErrCanceled.
func readHiddenFrom(r io.Reader) ([]byte, error) {
	var h hiddenLine
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 && h.feed(buf[0]) {
			break
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(h.buf) == 0 {
					return nil, ErrCanceled
				}
				break
			}
			return nil, err
		}
		if n == 0 {
			// The io.Reader contract permits (0, nil); without an escape a
			// stalled reader would spin this loop forever.
			return nil, io.ErrNoProgress
		}
	}
	if h.canceled {
		return nil, ErrCanceled
	}
	return h.buf, nil
}

// Password reads a hidden passphrase from the terminal. The prompt goes to
// stderr so it never pollutes piped stdout. The read runs in raw mode with
// ISIG off, so Ctrl-C arrives as data and cancels (ErrCanceled) instead of
// killing the process mid-read with the terminal left echo-less — the failure
// mode of x/term's ReadPassword, which keeps ISIG on and cannot restore
// termios from a signal. Returns ErrCanceled on empty input.
func Password(prompt string) (string, error) {
	if !Interactive() {
		return "", errors.New("interactive prompt requires a terminal")
	}
	fmt.Fprint(os.Stderr, prompt+" ")
	old, rawErr := term.MakeRaw(int(os.Stdin.Fd()))
	if rawErr != nil {
		// No fallback to term.ReadPassword: it performs the same termios
		// ioctls as MakeRaw, so any stdin MakeRaw rejects fails it too.
		fmt.Fprintln(os.Stderr)
		return "", fmt.Errorf("cannot put the terminal in raw mode: %w", rawErr)
	}
	pass, err := readHiddenFrom(os.Stdin)
	_ = term.Restore(int(os.Stdin.Fd()), old)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if len(pass) == 0 {
		return "", ErrCanceled
	}
	return string(pass), nil
}
