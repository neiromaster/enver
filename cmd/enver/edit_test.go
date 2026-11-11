package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/neiromaster/enver/internal/ui"
)

func TestNewEditStateSortedWithComments(t *testing.T) {
	prof := config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"B": "2", "A": "1"}}
	comments := map[string]string{"A": "the A"}
	s := plainState(t, "p", prof, comments, true)
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
	s := plainState(t, "p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
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
	s := plainState(t, "p", config.Profile{Env: map[string]string{"A": "1", "B": "2"}}, nil, false)
	s.deleteKey("A")
	if _, ok := s.find("A"); ok || len(s.entries) != 1 {
		t.Fatalf("delete failed: %+v", s.entries)
	}
}

func TestEditStateCanCommitInvariant(t *testing.T) {
	empty := plainState(t, "p", config.Profile{}, nil, false)
	if err := empty.canCommit(); err == nil {
		t.Fatal("empty non-extending profile should not commit")
	}
	withExtends := plainState(t, "p", config.Profile{Extends: config.Extends{"base"}}, nil, false)
	if err := withExtends.canCommit(); err != nil {
		t.Fatalf("extends-only should commit: %v", err)
	}
}

func TestEditStateDirtyDetection(t *testing.T) {
	// Freshly loaded: clean, including a comment and extends and default.
	s := plainState(t, "p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"A": "1"}},
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
	se := plainState(t, "p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
	se.extends = config.Extends{"base"}
	if !se.dirty() {
		t.Fatal("changed extends should be dirty")
	}

	// Toggled default from clean state.
	sd := plainState(t, "p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
	sd.isDefault = true
	if !sd.dirty() {
		t.Fatal("toggled default should be dirty")
	}

	// Pending profile deletion from clean state.
	sp := plainState(t, "p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
	sp.deleteProfile = true
	if !sp.dirty() {
		t.Fatal("pending delete should be dirty")
	}
}

func TestMenuOptionsDoneMarksUnsaved(t *testing.T) {
	s := plainState(t, "p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
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
	s := plainState(t, "p", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
	inherited := []ui.EnvEntry{{Key: "INH", Value: "y"}}
	labels := optionLabels(s.menuOptions(inherited, nil))
	for _, want := range []string{"OWN", "INH", "Add variable", "Change extends", "Done", "Delete variable", "Delete profile", "Set as default"} {
		if !strings.Contains(labels, want) {
			t.Fatalf("menu missing %q: %s", want, labels)
		}
	}
}

// TestMenuOptionsMaskSecretValues pins the masking policy: the edit menu is a
// display-only listing, so secret values mask there exactly as they do in
// show. Selecting a row still opens the full-value card.
func TestMenuOptionsMaskSecretValues(t *testing.T) {
	s := plainState(t, "p", config.Profile{Env: map[string]string{
		"API_SECRET": "hunter2", "PLAIN": "visible",
	}}, nil, false)
	labels := optionLabels(s.menuOptions(nil, nil))
	if strings.Contains(labels, "hunter2") {
		t.Fatalf("menu leaked a secret: %s", labels)
	}
	if !strings.Contains(labels, "visible") {
		t.Fatalf("non-secret value missing: %s", labels)
	}
}

// TestMenuOptionsMasksMarkedValue pins the mark's display half: a value whose
// key carries the secret mark masks even when neither the name nor the value
// trips the sensitivity heuristic, on own and inherited rows alike — and the
// same value stays visible without the mark.
func TestMenuOptionsMasksMarkedValue(t *testing.T) {
	marked := plainState(t, "p", config.Profile{
		Env:    map[string]string{"K": "hunter2"},
		Marked: map[string]bool{"K": true},
	}, nil, false)
	labels := optionLabels(marked.menuOptions([]ui.EnvEntry{{Key: "GH", Value: "hunter2", Secret: true}}, nil))
	if strings.Contains(labels, "hunter2") {
		t.Fatalf("menu leaked a marked value: %s", labels)
	}

	plain := plainState(t, "p", config.Profile{Env: map[string]string{"K": "hunter2"}}, nil, false)
	plainLabels := optionLabels(plain.menuOptions([]ui.EnvEntry{{Key: "GH", Value: "hunter2"}}, nil))
	if !strings.Contains(plainLabels, "hunter2") {
		t.Fatalf("an unmarked value must stay visible: %s", plainLabels)
	}
}

// TestMarkedRowsShowTheMark pins the label half of the mark: every picker that
// lists a variable spells a marked one ~KEY, while the row value stays the
// runtime name the commit writes.
func TestMarkedRowsShowTheMark(t *testing.T) {
	s := plainState(t, "p", config.Profile{
		Env:    map[string]string{"K": "hunter2hunter2", "PLAIN": "visible"},
		Marked: map[string]bool{"K": true},
	}, nil, false)

	for name, opts := range map[string][]ui.Option{
		"menu":    s.menuOptions(nil, nil),
		"secrets": secretsOptions(s, nil),
	} {
		row := findOption(t, opts, "K")
		if !strings.HasPrefix(row.Label, "~K = ") {
			t.Fatalf("%s row = %q, want the marked spelling", name, row.Label)
		}
		if plain := findOption(t, opts, "PLAIN"); strings.HasPrefix(plain.Label, "~") {
			t.Fatalf("%s row = %q, an unmarked key must not carry the mark", name, plain.Label)
		}
	}

	del := deleteVarOptions(s, nil)
	if got := findOption(t, del, "K"); got.Label != "~K" {
		t.Fatalf("delete row = %q, want ~K", got.Label)
	}
	if got := findOption(t, del, "PLAIN"); got.Label != "PLAIN" {
		t.Fatalf("delete row = %q, want PLAIN", got.Label)
	}
}

func TestParseMenuChoice(t *testing.T) {
	s := plainState(t, "p", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
	if kind, key := parseMenuChoice("OWN", s); kind != "own" || key != "OWN" {
		t.Fatalf("own choice: kind=%q key=%q", kind, key)
	}
	if kind, key := parseMenuChoice(actionDone, s); kind != "action" || key != actionDone {
		t.Fatalf("action choice: kind=%q key=%q", kind, key)
	}
	// An override row's Value is the bare key, so it classifies as "own".
	overrideState := plainState(t, "p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"SHADOW": "mine"}}, nil, false)
	if kind, key := parseMenuChoice("SHADOW", overrideState); kind != "own" || key != "SHADOW" {
		t.Fatalf("override choice: kind=%q key=%q", kind, key)
	}
}

func TestInheritedForStateReflectsWorkingExtends(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"FROM_BASE": "b"}},
	}}
	// Working copy starts with no extends: nothing inherited.
	s := plainState(t, "a", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
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
	s := plainState(t, "a", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"OWN": "mine"}}, nil, false)
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
	s := plainState(t, "a", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
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
	s := plainState(t, "a", config.Profile{Extends: config.Extends{"b"}, Env: map[string]string{"K": "v"}}, nil, false)
	s.extends = config.Extends{"b"} // a extending b, where b extends a
	if err := commitValidate(cfg, s); err == nil {
		t.Fatal("cycle not rejected")
	}
}

func TestApplyExtendsPickRejectsCycle(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"a":    {Env: map[string]string{"K": "v"}},
		"b":    {Extends: config.Extends{"a"}},
		"base": {},
	}}
	s := plainState(t, "a", config.Profile{}, nil, false)
	s.extends = config.Extends{"base"}
	err := applyExtendsPick(cfg, "a", &s, []string{"b"})
	if err == nil {
		t.Fatal("cycling pick accepted")
	}
	if len(s.extends) != 1 || s.extends[0] != "base" {
		t.Fatalf("state changed on rejected pick: %v", s.extends)
	}
}

func TestApplyExtendsPickAppliesAndClears(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{"base": {}}}
	s := plainState(t, "a", config.Profile{}, nil, false)
	if err := applyExtendsPick(cfg, "a", &s, []string{"base"}); err != nil {
		t.Fatalf("valid pick rejected: %v", err)
	}
	if len(s.extends) != 1 || s.extends[0] != "base" {
		t.Fatalf("pick not applied: %v", s.extends)
	}
	if err := applyExtendsPick(cfg, "a", &s, nil); err != nil {
		t.Fatalf("clear rejected: %v", err)
	}
	if s.extends != nil {
		t.Fatalf("clear left extends: %v", s.extends)
	}
}

func TestCommitValidateValidExtends(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"K": "v"}},
		"a":    {Env: map[string]string{"K2": "v2"}},
	}}
	s := plainState(t, "a", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"K2": "v2"}}, nil, false)
	if err := commitValidate(cfg, s); err != nil {
		t.Fatalf("valid extends rejected: %v", err)
	}
}

func TestCommitValidateEmpty(t *testing.T) {
	s := plainState(t, "a", config.Profile{}, nil, false)
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
	prof, isDefault, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Default: "p", Profiles: map[string]config.Profile{"p": prof}}
	s := plainState(t, "p", prof, prof.Comments, isDefault)
	// Edit A (keep comment), delete B, add C with a comment.
	s.upsert(ui.EnvEntry{Key: "A", Value: "1-new", Comment: "a-hint"})
	s.deleteKey("B")
	s.upsert(ui.EnvEntry{Key: "C", Value: "3", Comment: "c-hint"})
	if err := commitEdit(path, cfg, s, true, nil); err != nil {
		t.Fatalf("commitEdit: %v", err)
	}
	p, isDef, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	got := p.Comments
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
	s := plainState(t, "p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"SHADOW": "mine", "OWN": "x"}}, nil, false)
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
	s := plainState(t, "a", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
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

func TestDeleteVarOptionsMarksFences(t *testing.T) {
	s := plainState(t, "p",
		config.Profile{Env: map[string]string{"FKEY": "v", "OWN": "x"}, Unset: config.Unsets{"FKEY"}},
		nil, false)
	opts := deleteVarOptions(s, nil)
	fenced := findOption(t, opts, "FKEY")
	if fenced.Icon != ui.IconUnset || !strings.Contains(fenced.Label, "· unset") {
		t.Fatalf("fenced row = %q / %q, want ⊘ · unset", fenced.Icon, fenced.Label)
	}
	plain := findOption(t, opts, "OWN")
	if plain.Icon != "" || strings.Contains(plain.Label, "· unset") {
		t.Fatalf("unfenced row = %q / %q, want plain", plain.Icon, plain.Label)
	}
}

func TestOverrideKeySetIgnoresWorkingFences(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"SHADOW": "from-base"}},
	}}
	s := plainState(t, "p",
		config.Profile{Extends: config.Extends{"base"},
			Env:   map[string]string{"SHADOW": "mine"},
			Unset: config.Unsets{"SHADOW"}},
		nil, false)
	if !overrideKeySet(cfg, s)["SHADOW"] {
		t.Fatal("a fenced override should still count as parent-contributed")
	}
	// The resolved inherited view stays fence-aware — the two probes disagree on purpose.
	if got := inheritedForState(cfg, s); len(got) != 0 {
		t.Fatalf("fenced key leaked into the resolved inherited view: %+v", got)
	}
}

func TestSettleDefineFenceLiftsOnDecline(t *testing.T) {
	s := plainState(t, "p",
		config.Profile{Env: map[string]string{"A": "1"}, Unset: config.Unsets{"X", "FKEY"}}, nil, false)
	settleDefineFence(&s, "PLAIN")
	if !nameSetsEqual(s.unset, config.Unsets{"X", "FKEY"}) {
		t.Fatalf("unfenced define touched the list: %v", s.unset)
	}
}

func TestDeleteVarOptionsMarksOverrides(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"SHADOW": "from-base"}},
	}}
	s := plainState(t, "p", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"SHADOW": "mine", "OWN": "x"}}, nil, false)
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
	s := plainState(t, "p", config.Profile{Env: map[string]string{"OWN": "x"}}, nil, false)
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

func findOption(t *testing.T, opts []ui.Option, value string) ui.Option {
	t.Helper()
	for _, o := range opts {
		if o.Value == value {
			return o
		}
	}
	t.Fatalf("option %q not found", value)
	return ui.Option{}
}

func TestEditStateDirtyDetectsUnsetChanges(t *testing.T) {
	s := plainState(t, "p", config.Profile{Env: map[string]string{"A": "1"}, Unset: config.Unsets{"X"}}, nil, false)
	if s.dirty() {
		t.Fatal("freshly loaded unset list should be clean")
	}
	s.unset = append(s.unset, "Y")
	if !s.dirty() {
		t.Fatal("appended unset should be dirty")
	}
	s.unset = config.Unsets{"X"}
	if s.dirty() {
		t.Fatal("restored unset list should be clean again")
	}
	s.unset = nil
	if !s.dirty() {
		t.Fatal("removed unset fence should be dirty")
	}
}

func TestMenuOptionsManageUnsetsRow(t *testing.T) {
	s := plainState(t, "p", config.Profile{Env: map[string]string{"A": "1"}, Unset: config.Unsets{"X"}}, nil, false)
	opt := findOption(t, s.menuOptions(nil, nil), actionManageUnsets)
	if opt.Icon != ui.IconUnset {
		t.Fatalf("manage row icon = %q, want %q", opt.Icon, ui.IconUnset)
	}
	if !strings.Contains(opt.Label, "Manage unsets") || !strings.Contains(opt.Label, "(1)") {
		t.Fatalf("manage row label missing name/count: %q", opt.Label)
	}

	s0 := plainState(t, "p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
	if got := findOption(t, s0.menuOptions(nil, nil), actionManageUnsets).Label; strings.Contains(got, "(") {
		t.Fatalf("empty list should omit the count suffix: %q", got)
	}
}

func TestMenuOptionsMarksFencedOwnVar(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"SHADOW": "from-base"}},
	}}
	s := plainState(t, "p",
		config.Profile{Extends: config.Extends{"base"},
			Env:   map[string]string{"SHADOW": "mine", "PLAIN": "v"},
			Unset: config.Unsets{"SHADOW"}},
		nil, false)
	opts := s.menuOptions(listedInheritedForState(cfg, s), overrideKeySet(cfg, s))
	fenced := findOption(t, opts, "SHADOW")
	if !fenced.Dim {
		t.Fatal("fenced var should render as a dimmed row (fence outranks the override mark)")
	}
	if fenced.Icon != "" || strings.Contains(fenced.Label, "· unset") {
		t.Fatalf("fence is conveyed by dimming only, got icon %q / label %q", fenced.Icon, fenced.Label)
	}
	plain := findOption(t, opts, "PLAIN")
	if plain.Dim || plain.Icon != "" {
		t.Fatalf("unfenced own var should render plain, got %+v", plain)
	}
}

func TestMenuOptionsListsFencedInheritedDimmed(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"FROM_BASE": "b", "LIVE": "l"}},
	}}
	s := plainState(t, "a",
		config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"OWN": "x"},
			Unset: config.Unsets{"FROM_BASE"}},
		nil, false)
	listed := listedInheritedForState(cfg, s)
	if len(listed) != 2 {
		t.Fatalf("fenced inherited key must stay listed for display, got %+v", listed)
	}
	opts := s.menuOptions(listed, nil)
	fenced := findOption(t, opts, "inherited:FROM_BASE")
	if !fenced.Dim || fenced.Icon != ui.IconInherited {
		t.Fatalf("listed fenced inherited = %+v, want Dim with the inherited icon", fenced)
	}
	live := findOption(t, opts, "inherited:LIVE")
	if live.Dim {
		t.Fatal("an unfenced inherited key must not be dimmed")
	}
}

func TestListedInheritedSurvivesCarriedFence(t *testing.T) {
	// A load-built config parks every fence in Carried at merge time and leaves
	// Unset holding the overriding layer's fresh list — so a display probe that
	// clears only Unset still strips through Carried and the row never appears.
	cfg := config.Config{Profiles: map[string]config.Profile{
		"a": {Extends: config.Extends{"base"}, Env: map[string]string{"OWN": "x"},
			Carried: []config.CarriedFence{{Key: "FROM_BASE", Layer: config.LayerGlobal}}},
		"base": {Env: map[string]string{"FROM_BASE": "b", "LIVE": "l"}},
	}}
	s := plainState(t, "a", cfg.Profiles["a"], nil, false)
	if listed := listedInheritedForState(cfg, s); len(listed) != 2 {
		t.Fatalf("carried-fenced inherited keys dropped from display listing: %+v", listed)
	}
	if got := inheritedForState(cfg, s); len(got) != 1 || got[0].Key != "LIVE" {
		t.Fatalf("resolved view must keep honoring the carried fence, got %+v", got)
	}
}

func TestOverrideKeySetSurvivesCarriedFence(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"a": {Extends: config.Extends{"base"}, Env: map[string]string{"SHADOW": "mine"},
			Carried: []config.CarriedFence{{Key: "SHADOW", Layer: config.LayerGlobal}}},
		"base": {Env: map[string]string{"SHADOW": "from-base"}},
	}}
	s := plainState(t, "a", cfg.Profiles["a"], nil, false)
	if !overrideKeySet(cfg, s)["SHADOW"] {
		t.Fatal("a carried-fenced override should still count as parent-contributed")
	}
}

func TestInheritedForStateExcludesDeclaredUnset(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"FROM_BASE": "b"}},
	}}
	s := plainState(t, "a",
		config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"OWN": "x"},
			Unset: config.Unsets{"FROM_BASE"}},
		nil, false)
	if got := inheritedForState(cfg, s); len(got) != 0 {
		t.Fatalf("declared-unset inherited key still resolved: %+v", got)
	}
	s.unset = nil
	if got := inheritedForState(cfg, s); len(got) != 1 || got[0].Key != "FROM_BASE" {
		t.Fatalf("lifting the fence should re-expose inherited key, got %+v", got)
	}
}

func TestManageUnsetOptionsGroupsOrderAndDedupe(t *testing.T) {
	inherited := []ui.EnvEntry{{Key: "API_KEY", Value: "k"}, {Key: "CACHE_TTL", Value: "c"}}
	s := plainState(t, "p",
		config.Profile{Env: map[string]string{"RETRIES": "5", "OWN": "x"},
			Unset: config.Unsets{"CACHE_TTL", "ZZZ"}},
		nil, false)
	var vals []string
	for _, o := range manageUnsetOptions(s, inherited) {
		if o.Separator {
			continue
		}
		vals = append(vals, o.Value)
	}
	want := []string{"CACHE_TTL", "ZZZ", "OWN", "RETRIES", "API_KEY", actionCancel}
	if len(vals) != len(want) {
		t.Fatalf("option values = %v, want %v", vals, want)
	}
	for i := range want {
		if vals[i] != want[i] {
			t.Fatalf("option values[%d] = %q, want %q (full: %v)", i, vals[i], want[i], vals)
		}
	}
	declared := findOption(t, manageUnsetOptions(s, inherited), "CACHE_TTL")
	if declared.Icon != ui.IconUnset || !strings.Contains(declared.Label, "(declared here)") {
		t.Fatalf("declared option should carry ⊘ + hint, got %q / %q", declared.Icon, declared.Label)
	}
	if got := findOption(t, manageUnsetOptions(s, inherited), "RETRIES").Label; !strings.Contains(got, "(own)") {
		t.Fatalf("own candidate label missing (own): %q", got)
	}
	if got := findOption(t, manageUnsetOptions(s, inherited), "API_KEY").Label; !strings.Contains(got, "(inherited)") {
		t.Fatalf("inherited candidate label missing (inherited): %q", got)
	}
}

func TestPlanUnsetsSurvivorsAppendConflicts(t *testing.T) {
	own := map[string]bool{"B": true}
	next, conflicts := planUnsets(config.Unsets{"X"}, []string{"B", "X", "NEW"}, func(k string) bool { return own[k] })
	if !slices.Equal(next, config.Unsets{"X", "B", "NEW"}) {
		t.Fatalf("next = %v, want [X B NEW] (survivors keep order, adds append)", next)
	}
	if len(conflicts) != 1 || conflicts[0] != "B" {
		t.Fatalf("same-layer conflicts = %v, want [B]", conflicts)
	}
	// Re-planning the same set is stable with no fresh conflicts.
	next2, conflicts2 := planUnsets(next, []string{"B", "X", "NEW"}, func(k string) bool { return own[k] })
	if conflicts2 != nil || !slices.Equal(next2, config.Unsets{"X", "B", "NEW"}) {
		t.Fatalf("re-plan changed the list or reported conflicts: %v / %v", conflicts2, next2)
	}
}

func TestPlanUnsetsCollapsesDuplicatePicks(t *testing.T) {
	next, _ := planUnsets(nil, []string{"A", "A", "B", "A"}, func(string) bool { return false })
	if !slices.Equal(next, config.Unsets{"A", "B"}) {
		t.Fatalf("next = %v, want [A B]", next)
	}
}

func TestDropUnsetKeyStripsOnlyTarget(t *testing.T) {
	got := dropUnsetKey(config.Unsets{"A", "B", "C"}, "B")
	if !slices.Equal(got, config.Unsets{"A", "C"}) {
		t.Fatalf("drop = %v, want [A C]", got)
	}
	if kept := dropUnsetKey(config.Unsets{"A"}, "ZZZ"); !slices.Equal(kept, config.Unsets{"A"}) {
		t.Fatalf("missing key should be a no-op, got %v", kept)
	}
	if len(dropUnsetKey(nil, "A")) != 0 {
		t.Fatal("empty input should stay empty")
	}
}

func TestEditStateDirtyIgnoresUnsetReorder(t *testing.T) {
	s := plainState(t, "p",
		config.Profile{Env: map[string]string{"A": "1"}, Unset: config.Unsets{"FA", "FB"}}, nil, false)
	s.unset = config.Unsets{"FB", "FA"}
	if s.dirty() {
		t.Fatal("semantically inert unset reorder flagged as unsaved changes")
	}
	s.unset = config.Unsets{"FB"}
	if !s.dirty() {
		t.Fatal("a dropped fence was not detected through the reorder path")
	}
}

func TestCommitEditRoundTripsUnset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "p",
		config.Profile{Env: map[string]string{"A": "1"}, Comments: map[string]string{"A": "hint"},
			Unset: config.Unsets{"F1", "F2"}},
		false, false); err != nil {
		t.Fatal(err)
	}
	prof, isDefault, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Profiles: map[string]config.Profile{"p": prof}}
	s := plainState(t, "p", prof, prof.Comments, isDefault)
	s.upsert(ui.EnvEntry{Key: "A", Value: "1-new"})
	if err := commitEdit(path, cfg, s, false, nil); err != nil {
		t.Fatalf("commitEdit: %v", err)
	}
	gotProf, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(gotProf.Unset, config.Unsets{"F1", "F2"}) {
		t.Fatalf("unset not round-tripped through edit: %v", gotProf.Unset)
	}
}

// TestInheritedEntriesExcludesOwnKeys pins the read-only backdrop: keys the
// profile defines itself never show up as inherited rows. Membership goes
// through envname.Has so Windows case folding matches resolution.
func TestInheritedEntriesSortedByKey(t *testing.T) {
	out := inheritedEntries(
		map[string]string{"ZED": "1", "ALPHA": "2", "MID": "3"},
		nil,
		map[string]string{"MID": "own"},
	)
	if len(out) != 2 || out[0].Key != "ALPHA" || out[1].Key != "ZED" {
		t.Fatalf("got %v, want ALPHA then ZED", out)
	}
}

func TestInheritedEntriesExcludesOwnKeys(t *testing.T) {
	resolved := map[string]string{"A": "1", "B": "2", "C": "3"}
	own := map[string]string{"B": "over"}

	got := inheritedEntries(resolved, nil, own)

	keys := make([]string, 0, len(got))
	for _, e := range got {
		keys = append(keys, e.Key)
	}
	if !slices.Equal(keys, []string{"A", "C"}) {
		t.Errorf("inheritedEntries keys = %v, want [A C]", keys)
	}
}

// TestEditMarkMapAuthoritative pins the map handed to the writer: non-nil even
// when no entry is marked — nil would mean "no information" and hand the
// decision back to the file — and keyed by runtime name, never by the marked
// spelling.
func TestEditMarkMapAuthoritative(t *testing.T) {
	s := plainState(t, "p", config.Profile{
		Env:    map[string]string{"A": "1", "B": "2"},
		Marked: map[string]bool{"B": true},
	}, nil, false)
	m := s.markMap()
	if m == nil {
		t.Fatal("a profile with a mark must still hand the writer a non-nil map")
	}
	if !m["B"] || m["A"] || len(m) != 1 {
		t.Fatalf("markMap = %#v, want exactly the runtime name B", m)
	}
	if _, has := m["~B"]; has {
		t.Fatalf("markMap is keyed by the marked spelling: %#v", m)
	}
	empty := plainState(t, "p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false).markMap()
	if empty == nil || len(empty) != 0 {
		t.Fatalf("unmarked working copy = %#v, want a non-nil empty map", empty)
	}
}

// TestEditStateMarksSurviveCommit pins the carry end to end: a mark spelled on a
// key in the file reaches the working copy and comes back out of a commit that
// rewrites the whole env mapping.
func TestEditStateMarksSurviveCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "p", config.Profile{
		Env:    map[string]string{"K": "v", "PLAIN": "x"},
		Marked: map[string]bool{"K": true},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	prof, isDefault, ok, err := config.ReadProfile(path, "p")
	if err != nil || !ok {
		t.Fatalf("ReadProfile: ok=%v err=%v", ok, err)
	}
	s := plainState(t, "p", prof, prof.Comments, isDefault)
	if !s.entries[0].Secret {
		t.Fatalf("mark lost on load: %+v", s.entries)
	}
	if s.entries[1].Secret {
		t.Fatalf("unmarked key read as marked: %+v", s.entries)
	}
	if err := commitEdit(path, config.Config{Profiles: map[string]config.Profile{"p": prof}}, s, false, nil); err != nil {
		t.Fatalf("commitEdit: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "~K") {
		t.Fatalf("the secret mark did not survive the commit:\n%s", raw)
	}
	got, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Marked["K"] || got.Marked["PLAIN"] {
		t.Fatalf("marks after commit = %#v", got.Marked)
	}
}

// TestEditOverrideInheritsMark pins the seed half of the transaction: overriding
// an inherited marked key starts from its value and its mark, and the commit
// writes the override with that mark so the child does not silently declassify
// the variable.
func TestEditOverrideInheritsMark(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "base", config.Profile{
		Env:    map[string]string{"GH_TOKEN": "from-base"},
		Marked: map[string]bool{"GH_TOKEN": true},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(path, "work", config.Profile{
		Extends: config.Extends{"base"}, Env: map[string]string{"OWN": "1"},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	base, _, _, err := config.ReadProfile(path, "base")
	if err != nil {
		t.Fatal(err)
	}
	prof, isDefault, _, err := config.ReadProfile(path, "work")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Profiles: map[string]config.Profile{"base": base, "work": prof}}
	s := plainState(t, "work", prof, prof.Comments, isDefault)
	seed := overrideSeed(inheritedForState(cfg, s), nil, "GH_TOKEN")
	if !seed.Secret || seed.Value != "from-base" {
		t.Fatalf("override seed = %+v, want the inherited value and its mark", seed)
	}

	s.upsert(seed)
	if err := commitEdit(path, cfg, s, false, nil); err != nil {
		t.Fatalf("commitEdit: %v", err)
	}
	got, _, _, err := config.ReadProfile(path, "work")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Marked["GH_TOKEN"] {
		t.Fatalf("override written without its inherited mark: env=%#v marks=%#v", got.Env, got.Marked)
	}
}

// TestEditAddKeepsInheritedMark pins the Add row's half of the carry: adding a
// key that shadows a marked inherited variable with a freshly typed value must
// not declassify it, since the working copy's authoritative mark map leaves the
// writer no fallback to the ancestor.
func TestEditAddKeepsInheritedMark(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "base", config.Profile{
		Env:    map[string]string{"GH_TOKEN": "from-base"},
		Marked: map[string]bool{"GH_TOKEN": true},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(path, "work", config.Profile{
		Extends: config.Extends{"base"}, Env: map[string]string{"OWN": "1"},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	base, _, _, err := config.ReadProfile(path, "base")
	if err != nil {
		t.Fatal(err)
	}
	prof, isDefault, _, err := config.ReadProfile(path, "work")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Profiles: map[string]config.Profile{"base": base, "work": prof}}
	s := plainState(t, "work", prof, prof.Comments, isDefault)

	s.addEntry(ui.EnvEntry{Key: "GH_TOKEN", Value: "typed"}, listedInheritedForState(cfg, s))
	if err := commitEdit(path, cfg, s, false, nil); err != nil {
		t.Fatalf("commitEdit: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(raw), "~GH_TOKEN"); got != 2 {
		t.Fatalf("the added override was written unmarked (%d marked entries):\n%s", got, raw)
	}
	got, _, _, err := config.ReadProfile(path, "work")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Marked["GH_TOKEN"] {
		t.Fatalf("added key lost the inherited mark: env=%#v marks=%#v", got.Env, got.Marked)
	}
	if got.Env["GH_TOKEN"] != "typed" {
		t.Fatalf("added value did not land: %#v", got.Env)
	}
}

// TestEditAddKeepsOwnMark pins the other door an Add can declassify through:
// collecting a name the working copy already defines marked replaces that entry,
// so the own entry's mark — the closer authority — must ride into the collected
// one. The inherited set is nil here on purpose: nothing outside the copy can
// supply the answer.
func TestEditAddKeepsOwnMark(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "p", config.Profile{
		Env:    map[string]string{"K": "old", "OTHER": "1"},
		Marked: map[string]bool{"K": true},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	prof, isDefault, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	s := plainState(t, "p", prof, prof.Comments, isDefault)
	if cur, ok := s.find("K"); !ok || !cur.Secret {
		t.Fatalf("setup: own entry should load marked: %+v", s.entries)
	}

	s.addEntry(ui.EnvEntry{Key: "K", Value: "typed"}, nil)
	if err := commitEdit(path, config.Config{Profiles: map[string]config.Profile{"p": prof}}, s, false, nil); err != nil {
		t.Fatalf("commitEdit: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "~K") {
		t.Fatalf("re-adding an own marked key wrote it unmarked:\n%s", raw)
	}
	got, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Marked["K"] {
		t.Fatalf("own mark lost on re-add: env=%#v marks=%#v", got.Env, got.Marked)
	}
	if got.Env["K"] != "typed" {
		t.Fatalf("the typed value did not land: %#v", got.Env)
	}
	if got.Env["OTHER"] != "1" {
		t.Fatalf("an unrelated key did not survive: %#v", got.Env)
	}
}

// TestEditDeleteMatchesMarkedKey pins that entries hold the runtime name, so a
// deletion matches a key the file spells with a mark and takes the mark with it.
func TestEditDeleteMatchesMarkedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "p", config.Profile{
		Env:    map[string]string{"GH_TOKEN": "secret", "KEEP": "1"},
		Marked: map[string]bool{"GH_TOKEN": true},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	prof, isDefault, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	s := plainState(t, "p", prof, prof.Comments, isDefault)
	entry, ok := s.find("GH_TOKEN")
	if !ok || !entry.Secret {
		t.Fatalf("the runtime name does not carry the mark: %+v", s.entries)
	}

	s.deleteKey("GH_TOKEN")
	if err := commitEdit(path, config.Config{Profiles: map[string]config.Profile{"p": prof}}, s, false, nil); err != nil {
		t.Fatalf("commitEdit: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "GH_TOKEN") {
		t.Fatalf("deleted marked key survived, marked or not:\n%s", raw)
	}
	got, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, has := got.Env["GH_TOKEN"]; has {
		t.Fatalf("deleted key still resolves: %#v", got.Env)
	}
	if got.Env["KEEP"] != "1" {
		t.Fatalf("the surviving key did not make it through the commit: %#v", got.Env)
	}
}

// TestMenuErrorAction pins how a failed menu Select is classified: real
// errors propagate (exit stays loud), cancels — including wrapped
// ErrCanceled — only ask before discarding pending edits, and a clean cancel
// with nothing pending just exits.
func TestMenuErrorAction(t *testing.T) {
	boom := errors.New("tea exploded")
	if got, ask := menuErrorAction(boom, true); !errors.Is(got, boom) || ask {
		t.Errorf("real error must propagate, got (%v, ask=%v)", got, ask)
	}

	wrapped := fmt.Errorf("menu: %w", ui.ErrCanceled)
	if got, ask := menuErrorAction(wrapped, false); got != nil || ask {
		t.Errorf("clean wrapped cancel must exit nil, got (%v, ask=%v)", got, ask)
	}
	if got, ask := menuErrorAction(wrapped, true); got != nil || !ask {
		t.Errorf("wrapped cancel with pending edits must ask, got (%v, ask=%v)", got, ask)
	}

	if got, ask := menuErrorAction(ui.ErrCanceled, true); got != nil || !ask {
		t.Errorf("bare cancel with pending edits must ask, got (%v, ask=%v)", got, ask)
	}
}

// TestSecretsActionTogglesMarks pins the staging contract: a confirmed Secrets
// selection is the authoritative mark set for own entries, and the Encrypt-now
// row schedules the encrypt step without itself counting as an unsaved change.
func TestSecretsActionTogglesMarks(t *testing.T) {
	s := plainState(t, "p", config.Profile{Env: map[string]string{"A": "1", "B": "2"}}, nil, false)
	applySecretsPick(&s, []string{"A"}, nil, nil)
	if !s.entries[0].Secret || s.entries[1].Secret {
		t.Fatalf("selection [A] staged %+v, want A marked and B clear", s.entries)
	}
	if s.encryptNow {
		t.Fatal("a mark-only selection must not schedule encryption")
	}

	applySecretsPick(&s, []string{"B", actionEncryptNow}, nil, nil)
	if s.entries[0].Secret || !s.entries[1].Secret {
		t.Fatalf("selection [B] staged %+v, want B marked and A clear", s.entries)
	}
	if !s.encryptNow {
		t.Fatal("the Encrypt-now row must schedule the encrypt step")
	}

	scheduled := plainState(t, "p", config.Profile{
		Env:    map[string]string{"A": "1"},
		Marked: map[string]bool{"A": true},
	}, nil, false)
	applySecretsPick(&scheduled, append(secretsChecked(scheduled), actionEncryptNow), nil, nil)
	if !scheduled.encryptNow {
		t.Fatal("the Encrypt-now row did not set the flag")
	}
	if scheduled.dirty() {
		t.Fatal("scheduling encryption changes no entry and must not read as unsaved changes")
	}
}

// TestSecretsMenuListsInheritedKeys pins the picker's shape and its inherited
// half: own entries in working order, then the inherited keys the copy does not
// own, sorted, then the Encrypt-now row. A picked inherited key materializes as
// an own marked entry carrying the value and comment the picker showed.
func TestSecretsMenuListsInheritedKeys(t *testing.T) {
	s := plainState(t, "p", config.Profile{
		Env:    map[string]string{"OWN": "x", "K": "hunter2hunter2"},
		Marked: map[string]bool{"K": true},
		Unset:  config.Unsets{"OWN", "REGION"},
	}, nil, false)
	inherited := []ui.EnvEntry{
		{Key: "ZZZ", Value: "z"},
		{Key: "GH_TOKEN", Value: "from-base"},
		{Key: "REGION", Value: "eu-west-1"},
		{Key: "OWN", Value: "shadowed"},
	}
	opts := secretsOptions(s, inherited)

	var values []string
	for _, o := range opts {
		if o.Separator || o.Action {
			continue
		}
		values = append(values, o.Value)
	}
	want := []string{"K", "OWN", "GH_TOKEN", "REGION", "ZZZ", actionEncryptNow}
	if !slices.Equal(values, want) {
		t.Fatalf("picker values = %v, want %v", values, want)
	}

	marked := findOption(t, opts, "K")
	if marked.Icon != ui.IconSecret {
		t.Fatalf("a marked key must carry the secret glyph: %+v", marked)
	}
	if strings.Contains(marked.Label, "hunter2hunter2") {
		t.Fatalf("the picker leaked a marked value: %q", marked.Label)
	}
	if got := findOption(t, opts, "REGION"); !strings.Contains(got.Label, "eu-west-1") {
		t.Fatalf("a non-sensitive value must stay readable: %q", got.Label)
	}
	if got := findOption(t, opts, "GH_TOKEN"); strings.Contains(got.Label, "from-base") {
		t.Fatalf("the picker must mask what the menu listing masks: %q", got.Label)
	}
	for _, fenced := range []string{"OWN", "REGION"} {
		if got := findOption(t, opts, fenced); !got.Dim {
			t.Fatalf("a fenced key must read as dimmed, like the menu shows it: %+v", got)
		}
	}
	if got := findOption(t, opts, "K"); got.Dim {
		t.Fatalf("an unfenced key must not dim: %+v", got)
	}

	seed := secretsChecked(s)
	if !slices.Contains(seed, "K") {
		t.Fatalf("the picker must open with the current marks checked: %v", seed)
	}
	if _, owned := s.find("GH_TOKEN"); owned {
		t.Fatal("setup: GH_TOKEN must be inherited, not owned")
	}

	applySecretsPick(&s, append(seed, "GH_TOKEN"), inherited, map[string]string{"GH_TOKEN": "base token"})
	entry, ok := s.find("GH_TOKEN")
	if !ok || !entry.Secret || entry.Value != "from-base" || entry.Comment != "base token" {
		t.Fatalf("inherited pick = %+v, want an own marked entry seeded from the inherited row", entry)
	}
	if !s.markMap()["K"] {
		t.Fatalf("the picker's inherited half must not clear own marks: %#v", s.markMap())
	}
}

// TestSecretsEncryptRunsAfterCommit pins the order of the two writes: the Done
// commit lands the mark in the file, and the encrypt step that follows finds the
// marked plaintext there and seals it under the file's own key.
func TestSecretsEncryptRunsAfterCommit(t *testing.T) {
	dir := t.TempDir()
	saveGlobalFlags(t)
	t.Setenv("ENVER_KEY", "")
	keyPath := filepath.Join(dir, "key")
	if err := crypto.GenerateKey(keyPath, true); err != nil {
		t.Fatal(err)
	}
	globalFlags.keyPath = keyPath

	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "p", config.Profile{
		Env: map[string]string{"TOKEN": "plain-value", "OTHER": "visible"},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	prof, isDefault, ok, err := config.ReadProfile(path, "p")
	if err != nil || !ok {
		t.Fatalf("ReadProfile: ok=%v err=%v", ok, err)
	}
	s := plainState(t, "p", prof, prof.Comments, isDefault)
	applySecretsPick(&s, []string{"TOKEN", actionEncryptNow}, nil, nil)
	if err := commitEdit(path, config.Config{Profiles: map[string]config.Profile{"p": prof}}, s, false, nil); err != nil {
		t.Fatalf("commitEdit: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "~TOKEN") {
		t.Fatalf("the staged mark did not reach the file:\n%s", raw)
	}

	if err := encryptMarked(path, "p"); err != nil {
		t.Fatalf("encrypt step: %v", err)
	}
	got, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if !crypto.IsEncrypted(got.Env["TOKEN"]) {
		t.Fatalf("the marked value stayed plaintext: %#v", got.Env)
	}
	key, _, err := crypto.LoadKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := crypto.DecryptValue(got.Env["TOKEN"], key)
	if err != nil || plain != "plain-value" {
		t.Fatalf("round trip = %q, %v; want the committed plaintext", plain, err)
	}
	if crypto.IsEncrypted(got.Env["OTHER"]) || got.Env["OTHER"] != "visible" {
		t.Fatalf("an unmarked value must stay plaintext: %#v", got.Env)
	}
	if !got.Marked["TOKEN"] {
		t.Fatalf("the mark did not survive encryption: %#v", got.Marked)
	}
}

// TestSecretsEncryptWithoutKeyKeepsMarks pins the degraded encrypt step: with no
// key at all the marks stay saved, the file stays plaintext, and the command
// still succeeds — the mark is a hint, so a missing key is not a failure.
func TestSecretsEncryptWithoutKeyKeepsMarks(t *testing.T) {
	dir := t.TempDir()
	saveGlobalFlags(t)
	t.Setenv("ENVER_KEY", "")
	prevKeyFile := crypto.KeyFilePath
	crypto.KeyFilePath = func() string { return filepath.Join(dir, "missing-key") }
	t.Cleanup(func() { crypto.KeyFilePath = prevKeyFile })
	prevInteractive := app.Interactive
	app.Interactive = func() bool { return false }
	t.Cleanup(func() { app.Interactive = prevInteractive })

	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "p", config.Profile{
		Env:    map[string]string{"TOKEN": "plain-value"},
		Marked: map[string]bool{"TOKEN": true},
	}, false, false); err != nil {
		t.Fatal(err)
	}

	if err := encryptMarked(path, "p"); err != nil {
		t.Fatalf("a missing key must not fail the encrypt step: %v", err)
	}
	got, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Env["TOKEN"] != "plain-value" || !got.Marked["TOKEN"] {
		t.Fatalf("the mark must stay saved with the value plaintext: env=%#v marks=%#v", got.Env, got.Marked)
	}
}

// TestSecretsEncryptCancelledPassphraseKeepsMarks pins that declining the
// passphrase prompt is the same benign situation as having no key: recovery
// needs a salt, so the fixture keeps an encrypted value, and the marked
// plaintext plus its mark must come through untouched with no error.
func TestSecretsEncryptCancelledPassphraseKeepsMarks(t *testing.T) {
	dir := t.TempDir()
	saveGlobalFlags(t)
	t.Setenv("ENVER_KEY", "")
	prevKeyFile := crypto.KeyFilePath
	crypto.KeyFilePath = func() string { return filepath.Join(dir, "missing-key") }
	t.Cleanup(func() { crypto.KeyFilePath = prevKeyFile })
	prevInteractive := app.Interactive
	app.Interactive = func() bool { return true }
	t.Cleanup(func() { app.Interactive = prevInteractive })
	prevPrompt := app.PromptPassphrase
	app.PromptPassphrase = func(string) (string, error) { return "", ui.ErrCanceled }
	t.Cleanup(func() { app.PromptPassphrase = prevPrompt })

	path := filepath.Join(dir, "config.yaml")
	sealed := ciphertextV3(t, "old")
	if err := config.UpsertProfile(path, "p", config.Profile{
		Env:    map[string]string{"TOKEN": "plain-value", "SEALED": sealed},
		Marked: map[string]bool{"TOKEN": true},
	}, false, false); err != nil {
		t.Fatal(err)
	}

	if err := encryptMarked(path, "p"); err != nil {
		t.Fatalf("a declined passphrase must not fail the encrypt step: %v", err)
	}
	got, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Env["TOKEN"] != "plain-value" || !got.Marked["TOKEN"] {
		t.Fatalf("the mark must stay saved with the value plaintext: env=%#v marks=%#v", got.Env, got.Marked)
	}
	if got.Env["SEALED"] != sealed {
		t.Fatalf("the existing ciphertext must be left alone: %#v", got.Env)
	}
}

// TestSecretsEncryptWithEnvKeyKeepsMarks pins the saltless-key guard: an
// ENVER_KEY key can decrypt but not encrypt, so the encrypt step notes the
// plaintext rather than calling EncryptFile with a nil salt.
func TestSecretsEncryptWithEnvKeyKeepsMarks(t *testing.T) {
	dir := t.TempDir()
	saveGlobalFlags(t)
	t.Setenv("ENVER_KEY", crypto.EncodeKey(make([]byte, 32)))

	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "p", config.Profile{
		Env:    map[string]string{"TOKEN": "plain-value"},
		Marked: map[string]bool{"TOKEN": true},
	}, false, false); err != nil {
		t.Fatal(err)
	}

	if err := encryptMarked(path, "p"); err != nil {
		t.Fatalf("an ENVER_KEY run must not fail the encrypt step: %v", err)
	}
	got, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Env["TOKEN"] != "plain-value" || !got.Marked["TOKEN"] {
		t.Fatalf("the mark must stay saved with the value plaintext: env=%#v marks=%#v", got.Env, got.Marked)
	}
}

// plainState builds the working copy for a profile with no encrypted values, so
// a test about marks, fences, or pickers never needs a key.
func plainState(t *testing.T, name string, prof config.Profile, comments map[string]string, isDefault bool) editState {
	t.Helper()
	s, err := newEditState(name, prof, comments, isDefault, nil)
	if err != nil {
		t.Fatalf("newEditState: %v", err)
	}
	return s
}

// testKey and testSalt seal the edit tests' fixtures, so a crypter can be handed
// to the editor without touching the real key ladder.
var (
	testKey  = bytes.Repeat([]byte{0x2a}, 32)
	testSalt = []byte("0123456789abcdef")
)

func testCrypter() *valueCrypter {
	return &valueCrypter{Key: testKey, Salt: testSalt, Params: crypto.CurrentParams}
}

func sealedValue(t *testing.T, value string) string {
	t.Helper()
	enc, err := crypto.EncryptValueWithParams(value, testKey, testSalt, crypto.CurrentParams)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return enc
}

// TestEditDecryptsForDisplay pins the load half: an encrypted value reaches the
// editor as the plaintext the card can edit, carrying the flag that says so, and
// a plain sibling comes through untouched. The decrypted load must also leave the
// copy clean — an unchanged profile may never read as unsaved.
func TestEditDecryptsForDisplay(t *testing.T) {
	sealed := sealedValue(t, "plain-value")
	prof := config.Profile{Env: map[string]string{"TOKEN": sealed, "PLAIN": "visible"}}
	s, err := newEditState("p", prof, nil, false, testCrypter())
	if err != nil {
		t.Fatalf("newEditState: %v", err)
	}
	token, ok := s.find("TOKEN")
	if !ok || !token.Encrypted {
		t.Fatalf("encrypted entry = %+v, want it flagged Encrypted", token)
	}
	if token.Value != "plain-value" {
		t.Fatalf("card value = %q, want the decrypted plaintext", token.Value)
	}
	plain, _ := s.find("PLAIN")
	if plain.Encrypted || plain.Value != "visible" {
		t.Fatalf("plain entry = %+v, want it untouched and not Encrypted", plain)
	}
	if s.dirty() {
		t.Fatal("a freshly loaded encrypted profile must read as clean")
	}
}

// TestEditSaveReencrypts pins the save half: the plaintext the card showed goes
// back to the file as fresh ciphertext that decrypts to the edited text.
func TestEditSaveReencrypts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "p", config.Profile{
		Env: map[string]string{"TOKEN": sealedValue(t, "old-value"), "OTHER": "1"},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	prof, isDefault, ok, err := config.ReadProfile(path, "p")
	if err != nil || !ok {
		t.Fatalf("ReadProfile: ok=%v err=%v", ok, err)
	}
	c := testCrypter()
	s, err := newEditState("p", prof, prof.Comments, isDefault, c)
	if err != nil {
		t.Fatalf("newEditState: %v", err)
	}
	cur, _ := s.find("TOKEN")
	cur.Value = "new-value"
	s.upsert(cur)
	if err := commitEdit(path, config.Config{Profiles: map[string]config.Profile{"p": prof}}, s, false, c); err != nil {
		t.Fatalf("commitEdit: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "new-value") {
		t.Fatalf("the edited plaintext reached the file:\n%s", raw)
	}
	got, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	stored := got.Env["TOKEN"]
	if !crypto.IsEncrypted(stored) {
		t.Fatalf("the saved value is not ciphertext: %q", stored)
	}
	plain, err := crypto.DecryptValue(stored, testKey)
	if err != nil || plain != "new-value" {
		t.Fatalf("saved ciphertext decrypts to %q (%v), want the edited text", plain, err)
	}
	if got.Env["OTHER"] != "1" {
		t.Fatalf("an unrelated key did not survive: %#v", got.Env)
	}
}

// TestEditSaveWithoutKeyKeepsCiphertext pins the degrade: without a crypter the
// entry keeps the ciphertext it already holds and its flag, its card stays shut,
// and a commit touching another key writes that ciphertext back byte for byte.
func TestEditSaveWithoutKeyKeepsCiphertext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	sealed := sealedValue(t, "old-value")
	if err := config.UpsertProfile(path, "p", config.Profile{
		Env: map[string]string{"TOKEN": sealed, "OTHER": "1"},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	prof, isDefault, ok, err := config.ReadProfile(path, "p")
	if err != nil || !ok {
		t.Fatalf("ReadProfile: ok=%v err=%v", ok, err)
	}
	s, err := newEditState("p", prof, prof.Comments, isDefault, nil)
	if err != nil {
		t.Fatalf("newEditState: %v", err)
	}
	token, _ := s.find("TOKEN")
	if !token.Encrypted || token.Value != sealed {
		t.Fatalf("entry without a key = %+v, want the ciphertext kept and flagged", token)
	}
	if !editCardBlocked(token, nil) {
		t.Fatal("an encrypted entry with no key must not open its card")
	}
	other, _ := s.find("OTHER")
	other.Value = "2"
	s.upsert(other)
	if err := commitEdit(path, config.Config{Profiles: map[string]config.Profile{"p": prof}}, s, false, nil); err != nil {
		t.Fatalf("commitEdit: %v", err)
	}

	got, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Env["TOKEN"] != sealed {
		t.Fatalf("the ciphertext was rewritten behind the user's back: %q", got.Env["TOKEN"])
	}
	if got.Env["OTHER"] != "2" {
		t.Fatalf("the edited key did not land: %#v", got.Env)
	}
}

// TestEditCannotReencryptWithEnvKey pins the saltless guard: an ENVER_KEY key
// decrypts but cannot seal, so a save that would have to write a decrypted value
// back fails loudly and leaves the file exactly as it was.
func TestEditCannotReencryptWithEnvKey(t *testing.T) {
	dir := t.TempDir()
	saveGlobalFlags(t)
	t.Setenv("ENVER_KEY", crypto.EncodeKey(testKey))
	path := filepath.Join(dir, "config.yaml")
	sealed := sealedValue(t, "old-value")
	if err := config.UpsertProfile(path, "p", config.Profile{Env: map[string]string{"TOKEN": sealed}}, false, false); err != nil {
		t.Fatal(err)
	}
	prof, isDefault, ok, err := config.ReadProfile(path, "p")
	if err != nil || !ok {
		t.Fatalf("ReadProfile: ok=%v err=%v", ok, err)
	}
	c, err := crypterFor(path)
	if err != nil {
		t.Fatalf("crypterFor: %v", err)
	}
	if c.Salt != nil {
		t.Fatalf("ENVER_KEY must resolve a saltless crypter, got salt %x", c.Salt)
	}
	s, err := newEditState("p", prof, prof.Comments, isDefault, c)
	if err != nil {
		t.Fatalf("newEditState: %v", err)
	}
	cur, _ := s.find("TOKEN")
	cur.Value = "new-value"
	s.upsert(cur)

	err = commitEdit(path, config.Config{Profiles: map[string]config.Profile{"p": prof}}, s, false, c)
	if err == nil || !strings.Contains(err.Error(), "cannot encrypt") {
		t.Fatalf("commit error = %v, want a loud saltless-key refusal", err)
	}
	got, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Env["TOKEN"] != sealed {
		t.Fatalf("a failed commit must not touch the file: %q", got.Env["TOKEN"])
	}
}

// TestEditCrypterForProfileNeedsNoKey pins the prompt discipline: a profile with
// nothing encrypted never resolves a key, and an encrypted one whose key is
// absent degrades to a nil crypter instead of failing.
func TestEditCrypterForProfileNeedsNoKey(t *testing.T) {
	dir := t.TempDir()
	saveGlobalFlags(t)
	t.Setenv("ENVER_KEY", "")
	prevKeyFile := crypto.KeyFilePath
	crypto.KeyFilePath = func() string { return filepath.Join(dir, "missing-key") }
	t.Cleanup(func() { crypto.KeyFilePath = prevKeyFile })
	prevInteractive := app.Interactive
	app.Interactive = func() bool { return false }
	t.Cleanup(func() { app.Interactive = prevInteractive })

	path := filepath.Join(dir, "config.yaml")
	if err := config.UpsertProfile(path, "p", config.Profile{
		Env: map[string]string{"PLAIN": "1", "TOKEN": sealedValue(t, "sealed")},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	prof, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}

	c, err := crypterForProfile(path, config.Profile{Env: map[string]string{"PLAIN": "1"}})
	if c != nil || err != nil {
		t.Fatalf("a profile with no encrypted value = (%v, %v), want no key and no error", c, err)
	}
	c, err = crypterForProfile(path, prof)
	if c != nil || err != nil {
		t.Fatalf("an absent key must degrade = (%v, %v), want nil, nil", c, err)
	}
}

// TestEditCardBlockedWithoutCrypter pins the one thing that keeps a card shut:
// the flag alone is not enough — only the absence of a key is.
func TestEditCardBlockedWithoutCrypter(t *testing.T) {
	encrypted := ui.EnvEntry{Key: "K", Value: "ciphertext", Encrypted: true}
	if !editCardBlocked(encrypted, nil) {
		t.Fatal("an encrypted entry without a crypter must not open its card")
	}
	if editCardBlocked(encrypted, testCrypter()) {
		t.Fatal("a resolved crypter must open the card")
	}
	if editCardBlocked(ui.EnvEntry{Key: "K", Value: "v"}, nil) {
		t.Fatal("a plaintext entry needs no key")
	}
}

// TestEditMenuMasksEncryptedPlaintext pins the labels: the menu and the Secrets
// picker now hold decrypted values, so Encrypted has to mask them there — the
// flag replaces the placeholder both menus used to pass.
func TestEditMenuMasksEncryptedPlaintext(t *testing.T) {
	prof := config.Profile{Env: map[string]string{"K": sealedValue(t, "hunter2hunter2")}}
	s, err := newEditState("p", prof, nil, false, testCrypter())
	if err != nil {
		t.Fatalf("newEditState: %v", err)
	}
	entry, _ := s.find("K")
	if !entry.Encrypted || entry.Value != "hunter2hunter2" {
		t.Fatalf("setup: the entry must load decrypted and flagged, got %+v", entry)
	}
	labels := map[string]string{
		"menu":    optionLabels(s.menuOptions(nil, nil)),
		"secrets": optionLabels(secretsOptions(s, nil)),
	}
	for name, got := range labels {
		if strings.Contains(got, "hunter2hunter2") {
			t.Fatalf("the %s leaked a decrypted value: %s", name, got)
		}
	}
}

// TestEditAddKeepsEncryptedFlag pins the Add row's carry: no text field can
// express the encryption state, so re-adding an encrypted key must keep sealing
// the typed value instead of writing it to the file in the clear.
func TestEditAddKeepsEncryptedFlag(t *testing.T) {
	prof := config.Profile{Env: map[string]string{"K": sealedValue(t, "old")}}
	s, err := newEditState("p", prof, nil, false, testCrypter())
	if err != nil {
		t.Fatalf("newEditState: %v", err)
	}
	s.addEntry(ui.EnvEntry{Key: "K", Value: "typed"}, nil)
	entry, ok := s.find("K")
	if !ok || !entry.Encrypted || entry.Value != "typed" {
		t.Fatalf("re-added entry = %+v, want the typed value still Encrypted", entry)
	}
	env, err := s.envForWrite(testCrypter())
	if err != nil {
		t.Fatalf("envForWrite: %v", err)
	}
	plain, err := crypto.DecryptValue(env["K"], testKey)
	if err != nil || plain != "typed" {
		t.Fatalf("written value decrypts to %q (%v), want the typed text sealed", plain, err)
	}
}

// TestEditRenameKeepsEncryptedFlag pins the rename path a card drives: the flag
// rides with the entry, so the value is sealed under the new name.
func TestEditRenameKeepsEncryptedFlag(t *testing.T) {
	prof := config.Profile{Env: map[string]string{"K": sealedValue(t, "old")}}
	s, err := newEditState("p", prof, nil, false, testCrypter())
	if err != nil {
		t.Fatalf("newEditState: %v", err)
	}
	entry, _ := s.find("K")
	entry.Key = "RENAMED"
	s.deleteKey("K") // the card path renames by deleting the old key first
	s.upsert(entry)
	if _, ok := s.find("K"); ok {
		t.Fatal("setup: the old name must be gone")
	}
	renamed, _ := s.find("RENAMED")
	if !renamed.Encrypted {
		t.Fatalf("renamed entry = %+v, want the encryption state carried", renamed)
	}
	env, err := s.envForWrite(testCrypter())
	if err != nil {
		t.Fatalf("envForWrite: %v", err)
	}
	plain, err := crypto.DecryptValue(env["RENAMED"], testKey)
	if err != nil || plain != "old" {
		t.Fatalf("renamed value decrypts to %q (%v), want the sealed original", plain, err)
	}
}

// TestEditRefusesPlaintextOverSecretWithoutKey pins the boundary the task exists
// for: with no key the editor cannot seal anything, so it must not write a typed
// plaintext over an encrypted variable. The Add row is the only way to reach that
// state, since the card of an encrypted entry stays shut.
func TestEditRefusesPlaintextOverSecretWithoutKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	sealed := sealedValue(t, "old-value")
	if err := config.UpsertProfile(path, "p", config.Profile{Env: map[string]string{"TOKEN": sealed}}, false, false); err != nil {
		t.Fatal(err)
	}
	prof, isDefault, ok, err := config.ReadProfile(path, "p")
	if err != nil || !ok {
		t.Fatalf("ReadProfile: ok=%v err=%v", ok, err)
	}
	s, err := newEditState("p", prof, prof.Comments, isDefault, nil)
	if err != nil {
		t.Fatalf("newEditState: %v", err)
	}
	s.addEntry(ui.EnvEntry{Key: "TOKEN", Value: "typed"}, nil)

	err = commitEdit(path, config.Config{Profiles: map[string]config.Profile{"p": prof}}, s, false, nil)
	if err == nil || !strings.Contains(err.Error(), "plaintext") || !strings.Contains(err.Error(), "TOKEN") {
		t.Fatalf("commit error = %v, want a refusal naming TOKEN", err)
	}
	got, _, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.Env["TOKEN"] != sealed {
		t.Fatalf("the secret was overwritten: %q", got.Env["TOKEN"])
	}
}
