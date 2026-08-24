package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
)

func TestNewEditStateSortedWithComments(t *testing.T) {
	prof := config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"B": "2", "A": "1"}}
	comments := map[string]string{"A": "the A"}
	s := newEditState("p", prof, comments, true)
	if !s.extends.Has("base") || !s.isDefault || len(s.entries) != 2 {
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
	withExtends := newEditState("p", config.Profile{Extends: config.Extends{"base"}}, nil, false)
	if err := withExtends.canCommit(); err != nil {
		t.Fatalf("extends-only should commit: %v", err)
	}
}

func TestEditStateDirtyDetection(t *testing.T) {
	// Freshly loaded: clean, including a comment and extends and default.
	s := newEditState("p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"A": "1"}},
		map[string]string{"A": "c"}, true)
	if s.dirty() {
		t.Fatal("freshly loaded state should not be dirty")
	}

	// Net-zero edits: add then delete, modify then revert -> still clean.
	s.upsert(ui.EnvEntry{Key: "B", Value: "2"})
	s.deleteKey("B")
	if s.dirty() {
		t.Fatal("add-then-delete should be net clean")
	}
	s.upsert(ui.EnvEntry{Key: "A", Value: "1-new", Comment: "c"})
	if !s.dirty() {
		t.Fatal("modified value should be dirty")
	}
	s.upsert(ui.EnvEntry{Key: "A", Value: "1", Comment: "c"})
	if s.dirty() {
		t.Fatal("reverted value should be clean again")
	}

	s.deleteKey("A")
	if !s.dirty() {
		t.Fatal("deleted entry should be dirty")
	}

	// Changed extends from clean state.
	se := newEditState("p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
	se.extends = config.Extends{"base"}
	if !se.dirty() {
		t.Fatal("changed extends should be dirty")
	}

	// Toggled default from clean state.
	sd := newEditState("p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
	sd.isDefault = true
	if !sd.dirty() {
		t.Fatal("toggled default should be dirty")
	}

	// Pending profile deletion from clean state.
	sp := newEditState("p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
	sp.deleteProfile = true
	if !sp.dirty() {
		t.Fatal("pending delete should be dirty")
	}
}

func TestMenuOptionsDoneMarksUnsaved(t *testing.T) {
	s := newEditState("p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
	if got := doneLabelOf(s.menuOptions(nil, nil)); got != "Done" {
		t.Fatalf("clean Done label = %q, want %q", got, "Done")
	}
	s.upsert(ui.EnvEntry{Key: "A", Value: "2"})
	if got := doneLabelOf(s.menuOptions(nil, nil)); got != "Done • unsaved changes" {
		t.Fatalf("dirty Done label = %q, want unsaved marker", got)
	}
}

func doneLabelOf(opts []ui.Option) string {
	for _, o := range opts {
		if o.Value == actionDone {
			return o.Label
		}
	}
	return ""
}

func optionLabels(opts []ui.Option) string {
	var labels []string
	for _, o := range opts {
		labels = append(labels, o.Label)
	}
	return strings.Join(labels, "|")
}

func TestMenuOptionsContainsVarsInheritedAndActions(t *testing.T) {
	s := newEditState("p", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
	inherited := []ui.EnvEntry{{Key: "INH", Value: "y"}}
	labels := optionLabels(s.menuOptions(inherited, nil))
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
	// An override row's Value is the bare key, so it classifies as "own".
	overrideState := newEditState("p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"SHADOW": "mine"}}, nil, false)
	if kind, key := parseMenuChoice("SHADOW", overrideState); kind != "own" || key != "SHADOW" {
		t.Fatalf("override choice: kind=%q key=%q", kind, key)
	}
}

func TestInheritedForStateReflectsWorkingExtends(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"FROM_BASE": "b"}},
	}}
	// Working copy starts with no extends: nothing inherited.
	s := newEditState("a", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
	if got := inheritedForState(cfg, s); len(got) != 0 {
		t.Fatalf("expected no inherited before extends set, got %+v", got)
	}
	// User picks "extends base" inside the editor without committing.
	s.extends = config.Extends{"base"}
	got := inheritedForState(cfg, s)
	if len(got) != 1 || got[0].Key != "FROM_BASE" {
		t.Fatalf("expected FROM_BASE inherited after extends change, got %+v", got)
	}
}

func TestInheritedForStateReexposesDeletedShadow(t *testing.T) {
	// own OWN shadows base OWN; deleting the own copy should re-expose the inherited one.
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"OWN": "from-base"}},
	}}
	s := newEditState("a", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"OWN": "mine"}}, nil, false)
	if got := inheritedForState(cfg, s); len(got) != 0 {
		t.Fatalf("expected no inherited while own shadows, got %+v", got)
	}
	s.deleteKey("OWN")
	got := inheritedForState(cfg, s)
	if len(got) != 1 || got[0].Key != "OWN" || got[0].Value != "from-base" {
		t.Fatalf("expected inherited OWN after shadow deleted, got %+v", got)
	}
}

func TestInheritedForStateEmptyOnCycle(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"a": {Extends: config.Extends{"b"}},
		"b": {Extends: config.Extends{"a"}, Env: map[string]string{"K": "v"}},
	}}
	s := newEditState("a", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
	s.extends = config.Extends{"b"} // would cycle
	if got := inheritedForState(cfg, s); got != nil {
		t.Fatalf("expected nil inherited on pending cycle, got %+v", got)
	}
}

func TestCommitValidateCycleRejected(t *testing.T) {
	// a -> b -> a is a cycle.
	cfg := config.Config{Profiles: map[string]config.Profile{
		"a": {Extends: config.Extends{"b"}},
		"b": {Extends: config.Extends{"a"}},
	}}
	s := newEditState("a", config.Profile{Extends: config.Extends{"b"}, Env: map[string]string{"K": "v"}}, nil, false)
	s.extends = config.Extends{"b"} // a extending b, where b extends a
	if err := commitValidate(cfg, s); err == nil {
		t.Fatal("cycle not rejected")
	}
}

func TestCommitValidateValidExtends(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"K": "v"}},
		"a":    {Env: map[string]string{"K2": "v2"}},
	}}
	s := newEditState("a", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"K2": "v2"}}, nil, false)
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
		config.Profile{Env: map[string]string{"A": "1", "B": "2"}, Comments: map[string]string{"A": "a-hint", "B": "b-hint"}},
		true, false); err != nil {
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

func TestMenuOptionsMarksOverrides(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"SHADOW": "from-base"}},
	}}
	s := newEditState("p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"SHADOW": "mine", "OWN": "x"}}, nil, false)
	// Integration reality: SHADOW is an override, so inheritedForState excludes it.
	if got := inheritedForState(cfg, s); len(got) != 0 {
		t.Fatalf("shadowed key leaked into inherited set: %+v", got)
	}
	opts := s.menuOptions(inheritedForState(cfg, s), overrideKeySet(cfg, s))
	for _, o := range opts {
		switch o.Value {
		case "SHADOW":
			if o.Icon != ui.IconOverride {
				t.Fatalf("override SHADOW should carry IconOverride, got %q", o.Icon)
			}
		case "OWN":
			if o.Icon != "" {
				t.Fatalf("non-override OWN should have no icon, got %q", o.Icon)
			}
		}
	}
}

func TestProbeConfigCarriesWorkingExtendsAndEnv(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"FROM_BASE": "b"}},
	}}
	s := newEditState("a", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
	s.extends = config.Extends{"base"} // changed in-session, not yet committed
	probe := probeConfig(cfg, s)
	tp := probe.Profiles["a"]
	if !tp.Extends.Has("base") {
		t.Fatalf("probe extends = %q, want base", tp.Extends)
	}
	if tp.Env["OWN"] != "x" {
		t.Fatalf("probe own env lost OWN: %+v", tp.Env)
	}
	if probe.Profiles["base"].Env["FROM_BASE"] != "b" {
		t.Fatal("probe dropped an unrelated profile")
	}
}

func TestOverrideSeedFillsValueAndComment(t *testing.T) {
	inherited := []ui.EnvEntry{{Key: "FOO", Value: "inherited"}}
	seed := overrideSeed(inherited, map[string]string{"FOO": "the comment"}, "FOO")
	if seed.Key != "FOO" || seed.Value != "inherited" || seed.Comment != "the comment" {
		t.Fatalf("seed = %+v", seed)
	}
	// No comment resolved → empty comment, value still filled.
	seed2 := overrideSeed(inherited, nil, "FOO")
	if seed2.Value != "inherited" || seed2.Comment != "" {
		t.Fatalf("seed2 = %+v", seed2)
	}
	// Unknown key → no value filled.
	seed3 := overrideSeed(inherited, nil, "MISSING")
	if seed3.Value != "" {
		t.Fatalf("seed3 = %+v", seed3)
	}
}

func TestDeleteVarOptionsMarksOverrides(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"SHADOW": "from-base"}},
	}}
	s := newEditState("p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"SHADOW": "mine", "OWN": "x"}}, nil, false)
	opts := deleteVarOptions(s, overrideKeySet(cfg, s))
	find := func(val string) ui.Option {
		for _, o := range opts {
			if o.Value == val {
				return o
			}
		}
		t.Fatalf("option %q not found", val)
		return ui.Option{}
	}
	shadow := find("SHADOW")
	if shadow.Icon != ui.IconOverride {
		t.Fatalf("override SHADOW should carry IconOverride, got %q", shadow.Icon)
	}
	if !strings.Contains(shadow.Label, "→ inherited") {
		t.Fatalf("SHADOW label should hint revert, got %q", shadow.Label)
	}
	own := find("OWN")
	if own.Icon != "" {
		t.Fatalf("non-override OWN should have no icon, got %q", own.Icon)
	}
	if strings.Contains(own.Label, "→ inherited") {
		t.Fatalf("OWN label should not hint revert, got %q", own.Label)
	}
}

func TestMenuOptionsInheritedUsesIcon(t *testing.T) {
	s := newEditState("p", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
	inherited := []ui.EnvEntry{{Key: "INH", Value: "y"}}
	opts := s.menuOptions(inherited, nil)
	for _, o := range opts {
		if o.Value == "inherited:INH" {
			if o.Icon != ui.IconInherited {
				t.Fatalf("inherited option icon = %q, want %q", o.Icon, ui.IconInherited)
			}
			if strings.Contains(o.Label, "(inherited)") {
				t.Fatalf("inherited label should drop the suffix: %q", o.Label)
			}
			return
		}
	}
	t.Fatal("inherited option not found in menu")
}
