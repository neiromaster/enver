package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

func TestValidateProfileName(t *testing.T) {
	good := []string{"a", "anth", "glm-2", "prod_db", "A1", "0base"}
	bad := []string{"", "-x", "x y", "x!", ".y", "café"}
	for _, n := range good {
		if err := validateProfileName(n); err != nil {
			t.Errorf("good name %q rejected: %v", n, err)
		}
	}
	for _, n := range bad {
		if err := validateProfileName(n); err == nil {
			t.Errorf("bad name %q accepted", n)
		}
	}
}

func TestBuildProfile(t *testing.T) {
	entries := []ui.EnvEntry{
		{Key: "API_KEY", Value: "sk-x", Comment: "from vault"},
		{Key: "MODEL", Value: "claude-sonnet-5"},
	}
	prof, comments := buildProfile(config.Extends{"anth"}, entries)
	if !prof.Extends.Has("anth") {
		t.Fatalf("extends = %q, want anth", prof.Extends)
	}
	if prof.Env["API_KEY"] != "sk-x" || prof.Env["MODEL"] != "claude-sonnet-5" {
		t.Fatalf("env = %v", prof.Env)
	}
	if comments["API_KEY"] != "from vault" {
		t.Fatalf("comment missing/wrong: %v", comments)
	}
	if _, ok := comments["MODEL"]; ok {
		t.Fatal("empty comment should not be recorded")
	}
}

func TestBuildProfileExtendsPassesThrough(t *testing.T) {
	entries := []ui.EnvEntry{{Key: "A", Value: "1"}}
	multi, _ := buildProfile(config.Extends{"base", "ci"}, entries)
	if len(multi.Extends) != 2 || multi.Extends[0] != "base" || multi.Extends[1] != "ci" {
		t.Fatalf("extends = %q, want [base ci] (picked order preserved)", multi.Extends)
	}
	none, _ := buildProfile(nil, entries)
	if len(none.Extends) != 0 {
		t.Fatalf("empty extends = %q, want none written", none.Extends)
	}
}

// TestBuildProfileSplitsMark pins the profile composer's half of the mark: a key
// typed with one leading ~ writes its runtime name and a mark, and the marked
// form never reaches the env map. The card strips the ~ and flags the entry
// instead, so both spellings must land the same way.
func TestBuildProfileSplitsMark(t *testing.T) {
	p, _ := buildProfile(nil, []ui.EnvEntry{
		{Key: "~K", Value: "v"},
		{Key: "FLAGGED", Value: "f", Secret: true},
		{Key: "PLAIN", Value: "p"},
	})
	if p.Env["K"] != "v" || p.Env["FLAGGED"] != "f" || p.Env["PLAIN"] != "p" {
		t.Fatalf("env = %v, want the runtime names", p.Env)
	}
	if _, ok := p.Env["~K"]; ok {
		t.Fatalf("env = %v, the mark must never key the map", p.Env)
	}
	if !p.Marked["K"] || !p.Marked["FLAGGED"] {
		t.Fatalf("marked = %v, want K and FLAGGED", p.Marked)
	}
	if p.Marked["PLAIN"] {
		t.Fatalf("marked = %v, PLAIN must stay unmarked", p.Marked)
	}
}

func TestParentEnvForResolvesChain(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"A": "1"}},
		"ci":   {Env: map[string]string{"A": "2", "B": "3"}},
		"dev":  {Env: map[string]string{"A": "own", "C": "own"}, Unset: config.Unsets{"A"}},
	}}
	got, _, warns := parentEnvFor(cfg, "dev", config.Extends{"base", "ci"})
	if len(warns) != 0 {
		t.Fatalf("healthy chain must not warn: %v", warns)
	}
	if got["A"] != "2" || got["B"] != "3" {
		t.Fatalf("parentEnv = %v, want A=2 (later parent wins), B=3", got)
	}
	if _, ok := got["C"]; ok {
		t.Fatalf("parentEnv = %v, own key C must not leak into inherited", got)
	}
	if _, stripped := got["A"]; !stripped {
		t.Fatal("self's declared unset must not strip the parent backdrop")
	}
}

func TestParentEnvForEmptyExtendsIsEmpty(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"dev": {Env: map[string]string{"A": "own"}},
	}}
	got, _, warns := parentEnvFor(cfg, "dev", nil)
	if len(got) != 0 || len(warns) != 0 {
		t.Fatalf("parentEnv = %v, warns = %v, want empty", got, warns)
	}
}

func TestParentEnvForCycleIsEmpty(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Extends: config.Extends{"dev"}, Env: map[string]string{"A": "1"}},
		"dev":  {},
	}}
	got, _, warns := parentEnvFor(cfg, "dev", config.Extends{"base"})
	if len(got) != 0 {
		t.Fatalf("parentEnv = %v, want empty (pending cycle)", got)
	}
	if len(warns) == 0 {
		t.Fatal("pending cycle must be reported as a warning")
	}
}

func TestParentEnvForSkipsBrokenBranch(t *testing.T) {
	// base's ancestry is broken (ghost is gone); ci is healthy and must
	// survive into the backdrop even though the whole chain fails to resolve.
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Extends: config.Extends{"ghost"}, Env: map[string]string{"A": "1"}},
		"ci":   {Env: map[string]string{"B": "3"}},
	}}
	got, _, warns := parentEnvFor(cfg, "dev", config.Extends{"base", "ci"})
	if got["B"] != "3" {
		t.Fatalf("parentEnv = %v, want ci's B=3 to survive base's broken ancestry", got)
	}
	if _, ok := got["A"]; ok {
		t.Fatalf("parentEnv = %v, broken branch's keys must be absent", got)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "base") {
		t.Fatalf("warns = %v, want exactly one warning naming base", warns)
	}
}

// TestParentEnvForCarriesMarks pins the backdrop's mark set: the same probe that
// yields the inherited values yields their marks, on the whole-chain path and on
// the per-parent fallback, where a broken branch contributes neither and a later
// parent's unmarked definition clears an earlier one's mark.
func TestParentEnvForCarriesMarks(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"TOKEN": "t"}, Marked: map[string]bool{"TOKEN": true}},
		"ci":   {Env: map[string]string{"PLAIN": "p"}},
		"dev":  {},
	}}
	env, marked, warns := parentEnvFor(cfg, "dev", config.Extends{"base", "ci"})
	if len(warns) != 0 {
		t.Fatalf("healthy chain must not warn: %v", warns)
	}
	if env["TOKEN"] != "t" || env["PLAIN"] != "p" {
		t.Fatalf("env = %v", env)
	}
	if !marked["TOKEN"] || marked["PLAIN"] {
		t.Fatalf("marked = %v, want TOKEN marked and PLAIN not", marked)
	}

	broken := config.Config{Profiles: map[string]config.Profile{
		"base": {Extends: config.Extends{"ghost"}, Env: map[string]string{"A": "1", "K": "1"}, Marked: map[string]bool{"A": true, "K": true}},
		"ci":   {Env: map[string]string{"B": "3", "K": "2"}, Marked: map[string]bool{"B": true}},
	}}
	env2, marked2, warns2 := parentEnvFor(broken, "dev", config.Extends{"base", "ci"})
	if len(warns2) != 1 {
		t.Fatalf("warns = %v, want exactly the broken branch", warns2)
	}
	if env2["B"] != "3" || env2["K"] != "2" {
		t.Fatalf("env = %v, want the healthy parent's values", env2)
	}
	if !marked2["B"] {
		t.Fatalf("marked = %v, the healthy parent's mark must survive", marked2)
	}
	if marked2["A"] {
		t.Fatalf("marked = %v, a broken branch contributes no marks", marked2)
	}
	if marked2["K"] {
		t.Fatalf("marked = %v, ci defines K unmarked, so the nearer mention clears the mark", marked2)
	}
}

func TestBuildSummary(t *testing.T) {
	entries := []ui.EnvEntry{
		{Key: "PORT", Value: "5432"},
		{Key: "API_TOKEN", Value: "sk-abcdefghijklmnopqrstuvwxyz"},
	}
	parent := map[string]string{"API_TOKEN": "sk-old", "LOG_LEVEL": "info"}
	got := buildSummary(entries, parent, nil, nil)

	if got[0].Key != "PORT" || got[0].Kind != ui.EntryAdded {
		t.Fatalf("got[0] = %+v, want PORT/Added", got[0])
	}
	if got[1].Key != "API_TOKEN" || got[1].Kind != ui.EntryOverride {
		t.Fatalf("got[1] = %+v, want API_TOKEN/Override", got[1])
	}
	if got[1].Value == "sk-abcdefghijklmnopqrstuvwxyz" {
		t.Fatal("override value must be masked")
	}
	if got[2].Key != "LOG_LEVEL" || got[2].Kind != ui.EntryInherited {
		t.Fatalf("got[2] = %+v, want LOG_LEVEL/Inherited (API_TOKEN is shadowed)", got[2])
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(got), got)
	}
	if entries[1].Value != "sk-abcdefghijklmnopqrstuvwxyz" {
		t.Fatal("buildSummary must not mutate its input entries")
	}
}

// TestBuildSummaryMasksByMark pins the summary's display rule: a marked row is
// spelled ~KEY and masked, an inherited mark does the same for the inherited row,
// and neither value trips the name/value heuristic, so only the mark can be doing
// either. The override lookup and the own-key set keep the runtime name.
func TestBuildSummaryMasksByMark(t *testing.T) {
	const ownValue, inheritedValue = "plain-own-value", "plain-inherited-value"
	entries := []ui.EnvEntry{
		{Key: "OWN_MARKED", Value: ownValue, Secret: true},
		{Key: "SHADOWED", Value: "mine"},
		{Key: "SHADOWED_MARKED", Value: "mine", Secret: true},
	}
	parent := map[string]string{
		"INHERITED_MARKED": inheritedValue,
		"SHADOWED":         "theirs",
		"SHADOWED_MARKED":  "theirs",
	}

	got := buildSummary(entries, parent, map[string]bool{"INHERITED_MARKED": true}, nil)
	if len(got) != 4 {
		t.Fatalf("got %d entries, want 4: %+v", len(got), got)
	}
	if got[0].Key != "~OWN_MARKED" || strings.Contains(got[0].Value, ownValue) {
		t.Fatalf("a typed mark must spell and mask its own row: %+v", got[0])
	}
	if got[1].Kind != ui.EntryOverride || got[1].Key != "SHADOWED" {
		t.Fatalf("a shadowed row must stay an override under its runtime name: %+v", got[1])
	}
	if got[2].Kind != ui.EntryOverride || got[2].Key != "~SHADOWED_MARKED" {
		t.Fatalf("a marked shadowed row keeps both the mark and the override kind: %+v", got[2])
	}
	if got[3].Key != "~INHERITED_MARKED" || strings.Contains(got[3].Value, inheritedValue) {
		t.Fatalf("an inherited mark must spell and mask the inherited row: %+v", got[3])
	}
	if got[3].Kind != ui.EntryInherited {
		t.Fatalf("inherited row kind = %v: %+v", got[3].Kind, got[3])
	}

	plain := buildSummary(
		[]ui.EnvEntry{{Key: "OWN_MARKED", Value: ownValue}},
		map[string]string{"INHERITED_MARKED": inheritedValue}, nil, nil)
	if plain[0].Key != "OWN_MARKED" || plain[1].Key != "INHERITED_MARKED" {
		t.Fatalf("an unmarked row must stay bare: %+v", plain)
	}
	if plain[0].Value != ownValue || plain[1].Value != inheritedValue {
		t.Fatalf("unmarked values must stay readable: %+v", plain)
	}
}

// TestBuildSummaryMasksCiphertext pins the at-rest half: a value already sealed
// masks in the summary exactly as show masks it, own row and inherited row alike,
// whether or not the key carries a mark.
func TestBuildSummaryMasksCiphertext(t *testing.T) {
	sealed := "enc:v3:argon2id$m=65536,t=3,p=2$c2FsdA==$Y2lwaGVy"
	got := buildSummary(
		[]ui.EnvEntry{{Key: "OWN_SEALED", Value: sealed}},
		map[string]string{"INHERITED_SEALED": sealed}, nil, nil)
	for _, row := range got {
		if strings.Contains(row.Value, sealed) {
			t.Fatalf("a sealed value must never print raw: %+v", row)
		}
		if !strings.HasPrefix(row.Value, "enc:…") {
			t.Fatalf("a sealed value must mask like show masks it: %+v", row)
		}
	}
}

// TestBuildSummaryMarksWhatTheWriteKeeps pins the preview to the write it
// previews: an own row's mark is the one the commit keeps — the target profile's
// stored mark when it already holds the key, else the picked chain's — so a
// retype with a plain name can neither unmark a stored secret nor show a mark the
// write drops.
func TestBuildSummaryMarksWhatTheWriteKeeps(t *testing.T) {
	// PROJECT_PATH trips neither half of the name/value heuristic, so only the
	// mark can mask it.
	const key, typed = "PROJECT_PATH", "plain-typed-value"
	entries := []ui.EnvEntry{{Key: key, Value: typed}}
	noOther := config.Config{}

	// The target file already holds the key marked, as a previous add left it.
	path := filepath.Join(t.TempDir(), ".enver.yaml")
	if err := config.UpsertProfile(path, "work", config.Profile{
		Env:    map[string]string{key: "old"},
		Marked: map[string]bool{key: true},
	}, false, false); err != nil {
		t.Fatal(err)
	}
	stored, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := buildSummary(entries, map[string]string{}, nil, addMarks(&noOther, stored, "work", nil, entries))
	if len(sum) != 1 || sum[0].Key != "~"+key {
		t.Fatalf("a stored mark must survive a plain retype in the preview: %+v", sum)
	}
	if strings.Contains(sum[0].Value, typed) {
		t.Fatalf("the stored mark must mask the preview value: %+v", sum[0])
	}

	if err := commitAdd(path, "work", nil, entries, noOther, stored, false); err != nil {
		t.Fatalf("commitAdd: %v", err)
	}
	got, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Profiles["work"].Marked[key] {
		t.Fatalf("the committed file must keep the stored mark: %+v", got.Profiles["work"])
	}

	// Mirror: the chain marks the key, the target stores it plainly, and an own
	// entry outranks an inherited mark — so the preview stays bare exactly as the
	// write leaves it.
	other := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{key: "parent"}, Marked: map[string]bool{key: true}},
	}}
	own := config.Config{Profiles: map[string]config.Profile{
		"work": {Extends: config.Extends{"base"}, Env: map[string]string{key: "old"}},
	}}
	sum = buildSummary(entries, map[string]string{key: "parent"}, map[string]bool{key: true},
		addMarks(&other, own, "work", config.Extends{"base"}, entries))
	if len(sum) != 1 || sum[0].Key != key {
		t.Fatalf("an own entry outranks the inherited mark, so the preview must stay bare: %+v", sum)
	}
	if sum[0].Value != typed {
		t.Fatalf("an unmarked preview keeps its value readable: %+v", sum[0])
	}
}

func TestUpsertEntry(t *testing.T) {
	entries := []ui.EnvEntry{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}}
	entries = upsertEntry(entries, ui.EnvEntry{Key: "A", Value: "9"})
	if len(entries) != 2 || entries[0].Value != "9" {
		t.Fatalf("upsert should replace existing key: %+v", entries)
	}
	entries = upsertEntry(entries, ui.EnvEntry{Key: "C", Value: "3"})
	if len(entries) != 3 || entries[2].Key != "C" {
		t.Fatalf("upsert should append new key: %+v", entries)
	}
}

// TestParentEnvForResolvesThroughMergedParents pins the view rule: the
// backdrop resolves parents from the config it is given, and doAdd passes the
// merged view — the same one the cycle check uses — so a parent the cycle
// check resolved never comes back as hidden.
func TestParentEnvForResolvesThroughMergedParents(t *testing.T) {
	merged := config.Config{Profiles: map[string]config.Profile{
		"child": {Extends: config.Extends{"base"}},
		"base":  {Env: map[string]string{"B": "1"}},
	}}
	env, _, warns := parentEnvFor(merged, "newkid", config.Extends{"child"})
	if len(warns) != 0 {
		t.Fatalf("unexpected warns: %v", warns)
	}
	if env["B"] != "1" {
		t.Fatalf("env=%v, want inherited B=1", env)
	}
}

// TestAddMarksInheritAcrossLayers pins add's half of the write-side mark rule:
// an entry the picked chain marks is written marked, a key marked nowhere is
// not, and the target file's own unmarked entry outranks the inherited mark.
func TestAddMarksInheritAcrossLayers(t *testing.T) {
	other := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"GH_TOKEN": "parent"}, Marked: map[string]bool{"GH_TOKEN": true}},
	}}
	entries := []ui.EnvEntry{{Key: "GH_TOKEN", Value: "typed"}, {Key: "PLAIN", Value: "p"}}

	marks := addMarks(&other, config.Config{}, "work", config.Extends{"base"}, entries)
	if !marks["GH_TOKEN"] || marks["PLAIN"] {
		t.Fatalf("marks = %#v, want GH_TOKEN marked and PLAIN not", marks)
	}
	own := config.Config{Profiles: map[string]config.Profile{
		"work": {Env: map[string]string{"GH_TOKEN": "old"}},
	}}
	if marks := addMarks(&other, own, "work", config.Extends{"base"}, entries); marks["GH_TOKEN"] {
		t.Fatalf("the target's own unmarked entry must win: %#v", marks)
	}
}

// TestCommitAddWritesInheritedMark pins add's wiring at the file level, not just
// the helper: the profile the command commits carries the mark its picked parent
// supplies from the other layer, and a key nothing marks stays plain.
func TestCommitAddWritesInheritedMark(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".enver.yaml")
	other := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"GH_TOKEN": "parent"}, Marked: map[string]bool{"GH_TOKEN": true}},
	}}
	entries := []ui.EnvEntry{{Key: "GH_TOKEN", Value: "typed"}, {Key: "PLAIN", Value: "p"}}
	if err := commitAdd(path, "work", config.Extends{"base"}, entries, other, config.Config{}, false); err != nil {
		t.Fatalf("commitAdd: %v", err)
	}
	got, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Profiles["work"].Marked["GH_TOKEN"] {
		t.Fatalf("the inherited mark must reach the file: %#v", got.Profiles["work"])
	}
	if got.Profiles["work"].Marked["PLAIN"] {
		t.Fatalf("a key nothing marks must stay plain: %#v", got.Profiles["work"])
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "~GH_TOKEN") {
		t.Fatalf("file does not write the mark:\n%s", data)
	}
}

// TestCommitAddMergesTypedMarks pins the two mark answers as a merge, not a
// choice: a typed mark outranks a file that implies none, while an inherited
// mark survives an entry the user typed plainly — a plain entry carries no
// information, so it can neither mark nor unmark.
func TestCommitAddMergesTypedMarks(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".enver.yaml")
	other := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"GH_TOKEN": "parent"}, Marked: map[string]bool{"GH_TOKEN": true}},
	}}
	entries := []ui.EnvEntry{
		{Key: "GH_TOKEN", Value: "typed"},
		{Key: "PROJECT_PATH", Value: "/tmp", Secret: true},
		{Key: "~SECOND", Value: "s"},
	}
	if err := commitAdd(path, "work", config.Extends{"base"}, entries, other, config.Config{}, false); err != nil {
		t.Fatalf("commitAdd: %v", err)
	}
	got, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Profiles["work"].Marked["GH_TOKEN"] {
		t.Fatalf("the inherited mark must survive: %#v", got.Profiles["work"].Marked)
	}
	if !got.Profiles["work"].Marked["PROJECT_PATH"] {
		t.Fatalf("the typed mark must reach the file: %#v", got.Profiles["work"].Marked)
	}
	if !got.Profiles["work"].Marked["SECOND"] {
		t.Fatalf("a key typed with ~ must reach the file marked: %#v", got.Profiles["work"].Marked)
	}
	if _, ok := got.Profiles["work"].Env["~SECOND"]; ok {
		t.Fatalf("env = %#v, the mark must never key the map", got.Profiles["work"].Env)
	}
}

// TestAddRefusesToClobberCiphertextWithoutKey pins the missing-key gate: writing
// a typed value over a value only a key can read destroys that secret, so the add
// refuses and names the keys instead of overwriting them.
func TestAddRefusesToClobberCiphertextWithoutKey(t *testing.T) {
	dir := t.TempDir()
	kpath := filepath.Join(dir, "key")
	if err := crypto.GenerateKey(kpath, true); err != nil {
		t.Fatal(err)
	}
	key, salt, err := crypto.LoadKey(kpath)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := crypto.EncryptValueWithParams("hunter2", key, salt, crypto.CurrentParams)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".enver.yaml")
	if err := config.UpsertProfile(path, "work", config.Profile{Env: map[string]string{"PLAIN": "old"}}, false, false); err != nil {
		t.Fatal(err)
	}
	entries := []ui.EnvEntry{{Key: "TOKEN", Value: "typed"}, {Key: "PLAIN", Value: "typed"}, {Key: "~MARKED", Value: "typed"}}

	t.Setenv("ENVER_KEY", "")
	savedKeyPath := globalFlags.keyPath
	t.Cleanup(func() { globalFlags.keyPath = savedKeyPath })
	globalFlags.keyPath = ""
	savedKeyFile := crypto.KeyFilePath
	crypto.KeyFilePath = func() string { return filepath.Join(dir, "absent") }
	t.Cleanup(func() { crypto.KeyFilePath = savedKeyFile })

	if err := ciphertextClobberError(path, "work", entries); err != nil {
		t.Fatalf("a profile holding no ciphertext has nothing to clobber: %v", err)
	}
	// The file gains the sealed value after the command loaded it: the gate
	// answers from the file as it stands, not from the load snapshot.
	if err := config.UpsertProfile(path, "work", config.Profile{Env: map[string]string{"TOKEN": sealed}}, false, false); err != nil {
		t.Fatal(err)
	}
	err = ciphertextClobberError(path, "work", entries)
	const want = "refusing to overwrite the encrypted value of TOKEN: no key is available (`enver decrypt`, or set --key/ENVER_KEY)"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}

	globalFlags.keyPath = kpath
	if err := ciphertextClobberError(path, "work", entries); err != nil {
		t.Fatalf("with a key available the typed overwrite proceeds: %v", err)
	}

	globalFlags.keyPath = ""
	if err := ciphertextClobberError(path, "missing", entries); err != nil {
		t.Fatalf("a profile the file does not hold has nothing to clobber: %v", err)
	}
}

// TestAddCompletesExistingProfiles pins that add offers existing profile
// names like every other profile-argument mutator: an existing name is a
// valid re-seed for the extends/env flow.
func TestAddCompletesExistingProfiles(t *testing.T) {
	path := writeTempConfig(t, "dev", map[string]string{"A": "1"}, nil, true)
	withGlobalConfig(t, path)

	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")
	cmd.Flags().Bool("global", false, "")
	_ = cmd.Flags().Set("config", path)
	_ = cmd.Flags().Set("global", "true")

	if addCmd.ValidArgsFunction == nil {
		t.Fatal("add has no profile completion")
	}
	got, dir := addCmd.ValidArgsFunction(cmd, nil, "")
	if dir != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("dir=%v, want NoFileComp", dir)
	}
	found := false
	for _, c := range got {
		if c == "dev" {
			found = true
		}
	}
	if !found {
		t.Fatalf("completions = %v, want dev", got)
	}
}

// TestPromptProfileNameReprompts pins the shared prompted-name contract:
// invalid input re-prompts instead of hard-erroring, and only a failed read
// (EOF) aborts. rename and duplicate lean on the same helper.
func TestPromptProfileNameReprompts(t *testing.T) {
	var titles []string
	inputs := []string{"  ", "bad name!", "  ok-name "}
	i := 0
	read := func(title string) (string, error) {
		titles = append(titles, title)
		v := inputs[i]
		i++
		return v, nil
	}

	got, ok := promptProfileName("New name", read)
	if !ok {
		t.Fatal("valid third input must return ok")
	}
	if got != "ok-name" {
		t.Fatalf("got %q, want trimmed ok-name", got)
	}
	if len(titles) != 3 {
		t.Errorf("prompted %d times, want 3 (re-prompt on invalid)", len(titles))
	}
	for _, ti := range titles {
		if ti != "New name" {
			t.Errorf("prompt title %q must follow the caller", ti)
		}
	}
}

func TestPromptProfileNameAbortsOnReadFailure(t *testing.T) {
	if _, ok := promptProfileName("New name", func(string) (string, error) {
		return "", io.EOF
	}); ok {
		t.Fatal("a failed read must abort, not re-prompt forever")
	}
}
