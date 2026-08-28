package main

import (
	"path/filepath"
	"slices"
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

// TestMenuOptionsMaskSecretValues pins the masking policy: the edit menu is a
// display-only listing, so secret values mask there exactly as they do in
// show. Selecting a row still opens the full-value card.
func TestMenuOptionsMaskSecretValues(t *testing.T) {
	s := newEditState("p", config.Profile{Env: map[string]string{
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
	prof, isDefault, _, err := config.ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Default: "p", Profiles: map[string]config.Profile{"p": prof}}
	s := newEditState("p", prof, prof.Comments, isDefault)
	// Edit A (keep comment), delete B, add C with a comment.
	s.upsert(ui.EnvEntry{Key: "A", Value: "1-new", Comment: "a-hint"})
	s.deleteKey("B")
	s.upsert(ui.EnvEntry{Key: "C", Value: "3", Comment: "c-hint"})
	if err := commitEdit(path, cfg, s, true); err != nil {
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

func TestDeleteVarOptionsMarksFences(t *testing.T) {
	s := newEditState("p",
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
	s := newEditState("p",
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
	s := newEditState("p",
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
	s := newEditState("p", config.Profile{Env: map[string]string{"A": "1"}, Unset: config.Unsets{"X"}}, nil, false)
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
	s := newEditState("p", config.Profile{Env: map[string]string{"A": "1"}, Unset: config.Unsets{"X"}}, nil, false)
	opt := findOption(t, s.menuOptions(nil, nil), actionManageUnsets)
	if opt.Icon != ui.IconUnset {
		t.Fatalf("manage row icon = %q, want %q", opt.Icon, ui.IconUnset)
	}
	if !strings.Contains(opt.Label, "Manage unsets") || !strings.Contains(opt.Label, "(1)") {
		t.Fatalf("manage row label missing name/count: %q", opt.Label)
	}

	s0 := newEditState("p", config.Profile{Env: map[string]string{"A": "1"}}, nil, false)
	if got := findOption(t, s0.menuOptions(nil, nil), actionManageUnsets).Label; strings.Contains(got, "(") {
		t.Fatalf("empty list should omit the count suffix: %q", got)
	}
}

func TestMenuOptionsMarksFencedOwnVar(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"SHADOW": "from-base"}},
	}}
	s := newEditState("p",
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
	s := newEditState("a",
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
			Carried: config.Unsets{"FROM_BASE"}},
		"base": {Env: map[string]string{"FROM_BASE": "b", "LIVE": "l"}},
	}}
	s := newEditState("a", cfg.Profiles["a"], nil, false)
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
			Carried: config.Unsets{"SHADOW"}},
		"base": {Env: map[string]string{"SHADOW": "from-base"}},
	}}
	s := newEditState("a", cfg.Profiles["a"], nil, false)
	if !overrideKeySet(cfg, s)["SHADOW"] {
		t.Fatal("a carried-fenced override should still count as parent-contributed")
	}
}

func TestInheritedForStateExcludesDeclaredUnset(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"FROM_BASE": "b"}},
	}}
	s := newEditState("a",
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
	s := newEditState("p",
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
	s := newEditState("p",
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
	s := newEditState("p", prof, prof.Comments, isDefault)
	s.upsert(ui.EnvEntry{Key: "A", Value: "1-new"})
	if err := commitEdit(path, cfg, s, false); err != nil {
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
		map[string]string{"MID": "own"},
	)
	if len(out) != 2 || out[0].Key != "ALPHA" || out[1].Key != "ZED" {
		t.Fatalf("got %v, want ALPHA then ZED", out)
	}
}

func TestInheritedEntriesExcludesOwnKeys(t *testing.T) {
	resolved := map[string]string{"A": "1", "B": "2", "C": "3"}
	own := map[string]string{"B": "over"}

	got := inheritedEntries(resolved, own)

	keys := make([]string, 0, len(got))
	for _, e := range got {
		keys = append(keys, e.Key)
	}
	if !slices.Equal(keys, []string{"A", "C"}) {
		t.Errorf("inheritedEntries keys = %v, want [A C]", keys)
	}
}
