package ui

import (
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
