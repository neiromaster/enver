package main

import (
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
)

func TestNewEditStateSortedWithComments(t *testing.T) {
	prof := config.Profile{Extends: "base", Env: map[string]string{"B": "2", "A": "1"}}
	comments := map[string]string{"A": "the A"}
	s := newEditState("p", prof, comments, true)
	if s.extends != "base" || !s.isDefault || len(s.entries) != 2 {
		t.Fatalf("state = %+v", s)
	}
	if s.entries[0].Key != "A" || s.entries[1].Key != "B" {
		t.Fatalf("entries not sorted: %+v", s.entries)
	}
	if s.entries[0].Comment != "the A" {
		t.Fatalf("comment lost: %+v", s.entries)
	}
}

func TestEditStateUpsertUpdateAndAppend(t *testing.T) {
	s := newEditState("p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
	s.upsert(ui.EnvEntry{Key: "A", Value: "1-new", Comment: "c"})
	s.upsert(ui.EnvEntry{Key: "B", Value: "2"})
	if s.entries[0].Value != "1-new" || s.entries[0].Comment != "c" {
		t.Fatalf("update wrong: %+v", s.entries)
	}
	if len(s.entries) != 2 || s.entries[1].Key != "B" {
		t.Fatalf("append wrong: %+v", s.entries)
	}
}

func TestEditStateDeleteKey(t *testing.T) {
	s := newEditState("p", config.Profile{Env: map[string]string{"A": "1", "B": "2"}}, nil, false)
	s.deleteKey("A")
	if _, ok := s.find("A"); ok || len(s.entries) != 1 {
		t.Fatalf("delete failed: %+v", s.entries)
	}
}

func TestEditStateCanCommitInvariant(t *testing.T) {
	empty := newEditState("p", config.Profile{}, nil, false)
	if err := empty.canCommit(); err == nil {
		t.Fatal("empty non-extending profile should not commit")
	}
	withExtends := newEditState("p", config.Profile{Extends: "base"}, nil, false)
	if err := withExtends.canCommit(); err != nil {
		t.Fatalf("extends-only should commit: %v", err)
	}
}

func TestMenuOptionsContainsVarsInheritedAndActions(t *testing.T) {
	s := newEditState("p", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
	inherited := []ui.EnvEntry{{Key: "INH", Value: "y"}}
	opts := s.menuOptions(inherited)
	labels := strings.Join(func() []string {
		var l []string
		for _, o := range opts {
			l = append(l, o.Label)
		}
		return l
	}(), "|")
	for _, want := range []string{"OWN", "INH", "Add variable", "Change extends", "Done", "Delete variable", "Delete profile", "Set as default"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("menu missing %q: %s", want, labels)
		}
	}
}

func TestParseMenuChoice(t *testing.T) {
	s := newEditState("p", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
	if kind, key := parseMenuChoice("OWN", s); kind != "own" || key != "OWN" {
		t.Fatalf("own choice: kind=%q key=%q", kind, key)
	}
	if kind, key := parseMenuChoice(actionDone, s); kind != "action" || key != actionDone {
		t.Fatalf("action choice: kind=%q key=%q", kind, key)
	}
}
