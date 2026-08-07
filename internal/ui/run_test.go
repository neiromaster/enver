package ui

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
)

type noopModel struct{}

func (noopModel) Init() tea.Cmd                         { return nil }
func (n noopModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return n, nil }
func (noopModel) View() tea.View                        { return tea.NewView("") }
func (n noopModel) Canceled() bool                      { return false }

func TestRunNonTTYReturnsError(t *testing.T) {
	_, err := run(noopModel{})
	if err == nil {
		t.Fatal("expected non-TTY error, got nil")
	}
	if errors.Is(err, ErrCanceled) {
		t.Fatalf("non-TTY must not report ErrCanceled: %v", err)
	}
}

func TestErrCanceledExists(t *testing.T) {
	if !errors.Is(ErrCanceled, ErrCanceled) {
		t.Fatal("ErrCanceled must satisfy errors.Is with itself")
	}
}
