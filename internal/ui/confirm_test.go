package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func updConfirm(m *confirmModel, msg tea.Msg) *confirmModel {
	mm, _ := m.Update(msg)
	return mm.(*confirmModel)
}

func TestConfirmDefaultsAndToggle(t *testing.T) {
	m := newConfirmModel("Ok?", false)
	if m.value {
		t.Fatal("defaultYes=false should seed value false")
	}
	m = updConfirm(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if !m.value {
		t.Fatal("right did not toggle to true")
	}
	m = updConfirm(m, tea.KeyPressMsg{Text: "n"})
	if m.value {
		t.Fatal("n did not set false")
	}
	m = updConfirm(m, tea.KeyPressMsg{Text: "y"})
	if !m.value {
		t.Fatal("y did not set true")
	}
}

func TestConfirmSubmitAndCancel(t *testing.T) {
	m := newConfirmModel("Ok?", true)
	mm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !mm.(*confirmModel).submitted || !mm.(*confirmModel).value {
		t.Fatal("enter should submit the default true")
	}
	m2 := newConfirmModel("Ok?", true)
	mm2, _ := m2.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !mm2.(*confirmModel).canceled {
		t.Fatal("esc did not cancel")
	}
}
