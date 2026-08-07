package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestEnvCardBlankNameFinishes(t *testing.T) {
	m := newEnvCardModel(EnvEntry{})
	mm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	em := mm.(*envCardModel)
	if em.cursor != 0 {
		t.Fatalf("blank name should not advance cursor, got %d", em.cursor)
	}
	if em.result().Key != "" {
		t.Fatalf("blank name should yield empty key, got %q", em.result().Key)
	}
}

func updCard(m *envCardModel, msg tea.Msg) *envCardModel {
	mm, _ := m.Update(msg)
	return mm.(*envCardModel)
}

func TestEnvCardAdvancesAndSubmits(t *testing.T) {
	m := newEnvCardModel(EnvEntry{})
	m = updCard(m, tea.KeyPressMsg{Text: "K"})          // type into name
	m = updCard(m, tea.KeyPressMsg{Code: tea.KeyTab})   // name -> value
	m = updCard(m, tea.KeyPressMsg{Text: "V"})          // type into value
	m = updCard(m, tea.KeyPressMsg{Code: tea.KeyTab})   // value -> comment
	m = updCard(m, tea.KeyPressMsg{Code: tea.KeyEnter}) // submit on last
	if !m.submitted {
		t.Fatal("enter on last field should submit")
	}
	r := m.result()
	if r.Key != "K" || r.Value != "V" {
		t.Fatalf("result = %+v, want Key=K Value=V", r)
	}
}

func TestEnvCardCancel(t *testing.T) {
	m := newEnvCardModel(EnvEntry{})
	mm, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !mm.(*envCardModel).canceled {
		t.Fatal("esc did not cancel")
	}
}
