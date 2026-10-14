package ui

import (
	"errors"
	"io"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func updInput(m *inputModel, msg tea.Msg) *inputModel {
	mm, _ := m.Update(msg)
	return mm.(*inputModel)
}

func TestInputSubmitReturnsText(t *testing.T) {
	m := newInputModel("Name")
	m = updInput(m, tea.KeyPressMsg{Text: "bob"})
	m = updInput(m, tea.KeyPressMsg{Text: " "})
	m = updInput(m, tea.KeyPressMsg{Text: "j"})
	m = updInput(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.submitted {
		t.Fatal("not submitted")
	}
	if got := m.result(); got != "bob j" {
		t.Fatalf("result = %q, want %q", got, "bob j")
	}
}

func TestInputCancel(t *testing.T) {
	m := newInputModel("Name")
	m = updInput(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if !m.canceled {
		t.Fatal("esc did not cancel")
	}
}

func TestPasswordNonInteractive(t *testing.T) {
	prev := Interactive
	Interactive = func() bool { return false }
	t.Cleanup(func() { Interactive = prev })

	if _, err := Password("Enter passphrase:"); err == nil {
		t.Fatal("Password must error when stdin is not a terminal")
	}
}

func TestHiddenLineFeeding(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		want     string
		canceled bool
	}{
		{"enter ends", "pw\n", "pw", false},
		{"carriage return ends", "pw\r", "pw", false},
		{"backspace erases rune", "ab\x7f\n", "a", false},
		{"ctrl-h erases rune", "ab\bn\n", "an", false},
		{"ctrl-u clears", "junk\x15x\n", "x", false},
		{"ctrl-c cancels", "p\x03", "", true},
		{"ctrl-d cancels empty", "\x04", "", true},
		{"ctrl-d mid-line ignored", "p\x04q\n", "pq", false},
		{"utf-8 backspace erases rune", "па\x7f\n", "п", false},
		{"ctrl-w erases word", "hello world\x17\n", "hello ", false},
		{"ctrl-w eats trailing spaces then word", "hi  \x17\n", "", false},
		{"utf-8 ctrl-w erases word", "привет мир\x17\n", "привет ", false},
		{"ctrl-w on empty ignored", "\x17pw\n", "pw", false},
		{"ctrl-v ctrl-c inserts literal", "a\x16\x03b\n", "a\x03b", false},
		{"ctrl-v enter inserts newline", "a\x16\nb\n", "a\nb", false},
		{"ctrl-v backspace inserts literal", "a\x16\x7f\n", "a\x7f", false},
		{"ctrl-v quotes one byte only", "a\x16\x12\x12\n", "a\x12", false},
		{"ctrl-r ignored", "p\x12w\n", "pw", false},
		{"ctrl-s ignored", "p\x13w\n", "pw", false},
		{"ctrl-q ignored", "p\x11w\n", "pw", false},
		{"ctrl-o ignored", "p\x0fw\n", "pw", false},
	}
	for _, c := range cases {
		var h hiddenLine
		var got string
		for i := 0; i < len(c.in); i++ {
			if h.feed(c.in[i]) {
				break
			}
		}
		if !h.done {
			t.Fatalf("%s: line not done after %q", c.name, c.in)
		}
		if h.canceled != c.canceled {
			t.Fatalf("%s: canceled = %v, want %v", c.name, h.canceled, c.canceled)
		}
		if !h.canceled {
			got = string(h.buf)
			if got != c.want {
				t.Fatalf("%s: = %q, want %q", c.name, got, c.want)
			}
		}
	}
}

func TestReadHiddenFrom(t *testing.T) {
	got, err := readHiddenFrom(strings.NewReader("secret\n"))
	if err != nil || string(got) != "secret" {
		t.Fatalf("readHiddenFrom(line) = %q, %v; want secret", got, err)
	}
	got, err = readHiddenFrom(strings.NewReader("sec\x7fure\n"))
	if err != nil || string(got) != "seure" {
		t.Fatalf("readHiddenFrom(edited) = %q, %v; want seure", got, err)
	}
	if _, err = readHiddenFrom(strings.NewReader("\x03")); err != ErrCanceled {
		t.Fatalf("readHiddenFrom(ctrl-c) = %v, want ErrCanceled", err)
	}
	if _, err = readHiddenFrom(strings.NewReader("")); err != ErrCanceled {
		t.Fatalf("readHiddenFrom(eof-empty) = %v, want ErrCanceled", err)
	}
	got, err = readHiddenFrom(strings.NewReader("par"))
	if err != nil || string(got) != "par" {
		t.Fatalf("readHiddenFrom(eof-flush) = %q, %v; want par", got, err)
	}
}

// stalledReader hands out two zero-byte reads before its byte, then EOF: a
// reader obeying the io.Reader contract but making no progress at first.
type stalledReader struct{ reads int }

func (r *stalledReader) Read(p []byte) (int, error) {
	r.reads++
	switch {
	case r.reads <= 2:
		return 0, nil
	case r.reads == 3:
		p[0] = 'a'
		return 1, nil
	default:
		return 0, io.EOF
	}
}

func TestReadHiddenFromNoProgress(t *testing.T) {
	if _, err := readHiddenFrom(&stalledReader{}); !errors.Is(err, io.ErrNoProgress) {
		t.Fatalf("a reader stuck at zero-byte reads must fail with io.ErrNoProgress, got %v", err)
	}
}
