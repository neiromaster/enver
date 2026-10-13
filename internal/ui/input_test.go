package ui

import (
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
