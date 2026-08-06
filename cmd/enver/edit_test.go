package main

import (
	"path/filepath"
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

func TestCommitValidateCycleRejected(t *testing.T) {
	// a -> b -> a is a cycle.
	cfg := config.Config{Profiles: map[string]config.Profile{
		"a": {Extends: "b"},
		"b": {Extends: "a"},
	}}
	s := newEditState("a", config.Profile{Extends: "b", Env: map[string]string{"K": "v"}}, nil, false)
	s.extends = "b" // a extending b, where b extends a
	if err := commitValidate(cfg, s); err == nil {
		t.Fatal("cycle not rejected")
	}
}

func TestCommitValidateValidExtends(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"K": "v"}},
		"a":    {Env: map[string]string{"K2": "v2"}},
	}}
	s := newEditState("a", config.Profile{Extends: "base", Env: map[string]string{"K2": "v2"}}, nil, false)
	if err := commitValidate(cfg, s); err != nil {
		t.Fatalf("valid extends rejected: %v", err)
	}
}

func TestCommitValidateEmpty(t *testing.T) {
	s := newEditState("a", config.Profile{}, nil, false)
	if err := commitValidate(config.Config{}, s); err == nil {
		t.Fatal("empty non-extending profile should not validate")
	}
}

func TestCommitEditRoundTripsCommentsAndDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "p",
		config.Profile{Env: map[string]string{"A": "1", "B": "2"}},
		true, map[string]string{"A": "a-hint", "B": "b-hint"}); err != nil {
		t.Fatal(err)
	}
	prof, comments, isDefault, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Default: "p", Profiles: map[string]config.Profile{"p": prof}}
	s := newEditState("p", prof, comments, isDefault)
	// Edit A (keep comment), delete B, add C with a comment.
	s.upsert(ui.EnvEntry{Key: "A", Value: "1-new", Comment: "a-hint"})
	s.deleteKey("B")
	s.upsert(ui.EnvEntry{Key: "C", Value: "3", Comment: "c-hint"})
	if err := commitEdit(path, cfg, s, true); err != nil {
		t.Fatalf("commitEdit: %v", err)
	}
	_, got, isDef, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "a-hint" || got["C"] != "c-hint" {
		t.Fatalf("comments not preserved on surviving vars: %v", got)
	}
	if _, has := got["B"]; has {
		t.Fatal("deleted var's comment survived")
	}
	if !isDef {
		t.Fatal("default not preserved through edit")
	}
}
