package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/crypto"
)

func TestValidateFindsDanglingAndCycle(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"ok":     {Extends: Extends{"base"}},
		"base":   {Env: map[string]string{"K": "v"}},
		"dangle": {Extends: Extends{"ghost"}},
		"a":      {Extends: Extends{"b"}},
		"b":      {Extends: Extends{"a"}},
		"empty":  {},
	}}
	kinds := map[string]IssueKind{}
	for _, is := range Validate(cfg) {
		kinds[is.Profile] = is.Kind
	}
	if kinds["dangle"] != "dangling-extends" {
		t.Errorf("dangle = %q, want dangling-extends", kinds["dangle"])
	}
	if kinds["a"] != "cycle" {
		t.Errorf("a = %q, want cycle", kinds["a"])
	}
	if kinds["empty"] != "empty" {
		t.Errorf("empty = %q, want empty", kinds["empty"])
	}
	if _, bad := kinds["ok"]; bad {
		t.Error("healthy profile reported an issue")
	}
}

func TestValidateSeverityExit(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{"x": {Extends: Extends{"ghost"}}}}
	hasErr := false
	for _, is := range Validate(cfg) {
		if is.Severity == "error" {
			hasErr = true
		}
	}
	if !hasErr {
		t.Error("dangling extends should be an error severity")
	}
}

func TestValidateDeepDanglingNotLabeledCycle(t *testing.T) {
	// a -> b -> ghost ; b's dangling ref to ghost is the real issue.
	cfg := Config{Profiles: map[string]Profile{
		"a": {Extends: Extends{"b"}},
		"b": {Extends: Extends{"ghost"}},
	}}
	got := map[string]IssueKind{}
	for _, is := range Validate(cfg) {
		got[is.Profile] = is.Kind
	}
	if got["b"] != "dangling-extends" {
		t.Errorf("b = %q, want dangling-extends", got["b"])
	}
	if got["a"] == "cycle" {
		t.Error("a mislabeled as cycle (deep dangling should not be)")
	}
}

func TestValidateGlobalReportsMalformedFile(t *testing.T) {
	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	if err := os.WriteFile(globalPath, []byte("a: 'unterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateGlobal(globalPath); err == nil {
		t.Fatal("ValidateGlobal swallowed a parse error and reported the file valid")
	}
}

// TestValidatePlaintextSecret pins the marked-but-unencrypted warning: the mark
// promises encryption at rest, so a plaintext value under it is a health
// finding — a warning, never an error, because the config still works.
func TestValidatePlaintextSecret(t *testing.T) {
	plain := Config{Profiles: map[string]Profile{
		"p": {
			Env:    map[string]string{"PROJECT_PATH": "/home/anna"},
			Marked: map[string]bool{"PROJECT_PATH": true},
		},
	}}
	issues := Validate(plain)
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly one plaintext-secret warning", issues)
	}
	got := issues[0]
	if got.Kind != KindPlaintextSecret || got.Severity != SeverityWarning || got.Target != "PROJECT_PATH" {
		t.Fatalf("issue = %+v, want a plaintext-secret warning for PROJECT_PATH", got)
	}
	if want := `p: "PROJECT_PATH" is marked secret but stored in plaintext`; got.String() != want {
		t.Errorf("issue text = %q, want %q", got.String(), want)
	}

	enc, err := crypto.EncryptValueWithParams("/home/anna", make([]byte, 32), []byte("0123456789abcdef"), crypto.CurrentParams)
	if err != nil {
		t.Fatal(err)
	}
	encrypted := Config{Profiles: map[string]Profile{
		"p": {
			Env:    map[string]string{"PROJECT_PATH": enc},
			Marked: map[string]bool{"PROJECT_PATH": true},
		},
	}}
	for _, is := range Validate(encrypted) {
		if is.Kind == KindPlaintextSecret {
			t.Fatalf("encrypted secret flagged as plaintext: %+v", is)
		}
		if is.Severity == SeverityError {
			t.Fatalf("a marked profile must not raise errors: %+v", is)
		}
	}
}

func TestValidateGlobalIsolated(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	// broken extends dev, but dev is not defined in the global file.
	if err := os.WriteFile(globalPath, []byte("profiles:\n  broken:\n    extends: dev\n    env:\n      A: \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// ValidateGlobal loads the global file alone, so "dev" dangles and the issue
	// is attributed to the global scope.
	issues, err := ValidateGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, is := range issues {
		if is.Profile == "broken" && is.Kind == "dangling-extends" && is.Target == "dev" && is.File == "global" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ValidateGlobal did not flag broken→dev in isolation")
	}

	// Over the merged config — where dev exists (e.g. from a local layer) — the
	// same extends resolves fine and Validate must not flag it.
	merged := Config{Profiles: map[string]Profile{
		"broken": {Extends: Extends{"dev"}, Env: map[string]string{"A": "1"}},
		"dev":    {Env: map[string]string{"X": "2"}},
	}}
	for _, is := range Validate(merged) {
		if is.Profile == "broken" && is.Kind == "dangling-extends" {
			t.Fatalf("merged Validate should not flag broken when dev is present: %v", is)
		}
	}
}

// TestValidateCrossLayerFenceIsNotContradictory pins the layer rule: a local
// unset fencing a global definition is the documented pattern; only a
// definition and unset from the same layer contradict one file's profile.
func TestValidateCrossLayerFenceIsNotContradictory(t *testing.T) {
	global := Config{Profiles: map[string]Profile{"p": {
		Env: map[string]string{"A": "from-global", "B": "keep"},
	}}}
	local := Config{Profiles: map[string]Profile{"p": {Unset: []string{"A"}}}}
	for _, is := range Validate(Merge(global, local, LayerGlobal, LayerLocal)) {
		if is.Profile == "p" {
			t.Errorf("cross-layer fence reported as %s, want silence:\n%v", is.Kind, is)
		}
	}

	// Same-layer pairs still warn: single file first…
	sameFile := Config{Profiles: map[string]Profile{"p": {
		Env: map[string]string{"A": "1"}, Unset: []string{"A"},
	}}}
	if !hasIssue(Validate(sameFile), "contradictory-unset") {
		t.Error("same-profile define+unset must stay a contradictory-unset warning")
	}
	// …and a local layer that both defines and unsets the key.
	localBoth := Config{Profiles: map[string]Profile{"p": {
		Env: map[string]string{"A": "local"}, Unset: []string{"A"},
	}}}
	if !hasIssue(Validate(Merge(global, localBoth, LayerGlobal, LayerLocal)), "contradictory-unset") {
		t.Error("a local define+unset pair must stay a contradictory-unset warning")
	}
}

func TestValidateOwn(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"empty":  {},
		"plain":  {Env: map[string]string{"K": "v"}},
		"marked": {Env: map[string]string{"S": "x"}, Marked: map[string]bool{"S": true}},
		"fence":  {Env: map[string]string{"K": "v"}, Unset: []string{"K"}},
	}}
	kinds := map[string]IssueKind{}
	for _, is := range ValidateOwn(cfg) {
		kinds[is.Profile] = is.Kind
	}
	for p, want := range map[string]IssueKind{"empty": KindEmpty, "marked": KindPlaintextSecret, "fence": KindContradictoryUnset} {
		if kinds[p] != want {
			t.Errorf("%s = %s, want %s", p, kinds[p], want)
		}
	}
	if _, ok := kinds["plain"]; ok {
		t.Error("a healthy profile was reported")
	}
}

func TestIssueReasonMarkedEmpty(t *testing.T) {
	is := Issue{Kind: KindMarkedEmpty, Severity: SeverityWarning}
	if is.Severity != SeverityWarning || is.Reason() != "marked for publication but declares neither profiles nor remotes" {
		t.Fatalf("marked-empty issue = %s / %q", is.Severity, is.Reason())
	}
}

func hasIssue(issues []Issue, kind IssueKind) bool {
	for _, is := range issues {
		if is.Kind == kind {
			return true
		}
	}
	return false
}

// TestValidateRemoteDanglingErrors pins the grading every dangling extends gets:
// strict scoping leaves no authorship to grade, because a remote file cannot
// name a parent outside its own namespace, so a reference no profile defines is
// always an error — whoever declared it. The pull hint survives: a parent whose
// prefix is a declared but unpulled record still names the pull that supplies it.
func TestValidateRemoteDanglingErrors(t *testing.T) {
	t.Run("remote-declared profile is an error", func(t *testing.T) {
		cfg := Config{
			Profiles: map[string]Profile{
				"llm/qwen": {Extends: Extends{"base"}, Env: map[string]string{"MODEL": "qwen"}},
			},
			ProfileLayer: map[string]string{"llm/qwen": RemoteLayer("llm")},
		}
		issues := Validate(cfg)
		if len(issues) != 1 {
			t.Fatalf("issues = %+v, want exactly the dangling finding", issues)
		}
		got := issues[0]
		if got.Kind != KindDanglingExtends || got.Target != "base" {
			t.Fatalf("issue = %+v, want dangling-extends on base", got)
		}
		if got.Severity != SeverityError {
			t.Fatalf("severity = %q, want error: no layer can legitimately declare a parent nobody defines", got.Severity)
		}
		if want := `llm/qwen: extends "base" which does not exist`; got.String() != want {
			t.Errorf("issue text = %q, want %q", got.String(), want)
		}
	})

	t.Run("an unpulled provider adds the pull hint", func(t *testing.T) {
		defer remotesDirIn(t.TempDir())()
		cfg := Config{
			Profiles: map[string]Profile{
				"llm/qwen": {Extends: Extends{"other/base"}},
			},
			ProfileLayer: map[string]string{"llm/qwen": RemoteLayer("llm")},
			Remotes:      map[string]RemoteRecord{"other": {URL: "https://example.com/other.yaml"}},
		}
		issues := Validate(cfg)
		if len(issues) != 1 {
			t.Fatalf("issues = %+v, want one dangling error", issues)
		}
		if got := issues[0]; got.Severity != SeverityError {
			t.Errorf("severity = %q, want error", got.Severity)
		} else if !strings.Contains(got.String(), `enver pull other`) {
			t.Errorf("issue text %q lacks the pull that would supply the parent", got.String())
		}
	})

	t.Run("a pulled provider adds no pull hint", func(t *testing.T) {
		defer remotesDirIn(t.TempDir())()
		if err := WriteRemoteContent("other", []byte("profiles:\n  base:\n    env:\n      K: v\n")); err != nil {
			t.Fatal(err)
		}
		cfg := Config{
			Profiles: map[string]Profile{
				"llm/qwen": {Extends: Extends{"other/base"}},
			},
			ProfileLayer: map[string]string{"llm/qwen": RemoteLayer("llm")},
			Remotes:      map[string]RemoteRecord{"other": {URL: "https://example.com/other.yaml"}},
		}
		issues := Validate(cfg)
		if len(issues) != 1 {
			t.Fatalf("issues = %+v, want one dangling error", issues)
		}
		if got := issues[0].String(); strings.Contains(got, "enver pull") {
			t.Errorf("issue text %q promises a pull that cannot supply a missing upstream profile", got)
		}
	})

	t.Run("user-declared dangling stays an error", func(t *testing.T) {
		defer remotesDirIn(t.TempDir())()
		cfg := Config{
			Profiles: map[string]Profile{"mine": {Extends: Extends{"other/base"}}},
			Remotes:  map[string]RemoteRecord{"other": {URL: "https://example.com/other.yaml"}},
		}
		issues := Validate(cfg)
		if len(issues) != 1 {
			t.Fatalf("issues = %+v, want exactly the dangling finding", issues)
		}
		got := issues[0]
		if got.Severity != SeverityError {
			t.Fatalf("severity = %q, want error: the user's own declaration is theirs to fix", got.Severity)
		}
		if !strings.Contains(got.String(), `enver pull other`) {
			t.Errorf("issue text %q lacks the pull that would supply the unpulled parent", got.String())
		}
	})
}

// TestValidateGlobalOverRemotes pins what the isolated-global pass audits: the
// global file over the remote layers it sits on, never the local file. A global
// profile extending a remote-provided profile resolves through the layers and
// must not read as dangling; a global profile naming a local-only or nonexistent
// parent stays this pass's error; remote content's own findings stay with the
// merged pass.
func TestValidateGlobalOverRemotes(t *testing.T) {
	defer remotesDirIn(t.TempDir())()
	if err := WriteRemoteContent("llm", []byte("profiles:\n  base:\n    env:\n      K: v\n")); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, LocalFilename), []byte("profiles:\n  localprov:\n    env:\n      L: v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(chdir(t, proj))

	globalPath := filepath.Join(t.TempDir(), "global.yaml")
	body := "remotes:\n  llm:\n    url: https://example.com/llm.yaml\n" +
		"profiles:\n" +
		"  wrapper:\n    extends: llm/base\n" +
		"  needslocal:\n    extends: localprov\n" +
		"  mine:\n    extends: ghost\n"
	if err := os.WriteFile(globalPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	issues, err := ValidateGlobal(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	byProfile := map[string]Issue{}
	for _, is := range issues {
		if is.File != "global" {
			t.Fatalf("issue %+v attributed to the merged view, want this pass", is)
		}
		byProfile[is.Profile] = is
	}
	if _, flagged := byProfile["wrapper"]; flagged {
		t.Fatalf("a global profile extending a pulled remote's profile was flagged: %+v", byProfile["wrapper"])
	}
	if _, flagged := byProfile["llm/base"]; flagged {
		t.Fatalf("remote content was attributed to the global pass: %+v", byProfile["llm/base"])
	}
	for _, name := range []string{"needslocal", "mine"} {
		got, flagged := byProfile[name]
		if !flagged || got.Kind != KindDanglingExtends || got.Severity != SeverityError {
			t.Fatalf("%s: issue = %+v, want a dangling error: a local-only or unknown parent breaks this file", name, got)
		}
	}
}

// TestProfileNotFoundPullHint pins the hint a miss carries when the name is a
// prefixed profile of a declared but unpulled remote: show, x, and every other
// read path report the pull that supplies it, and no pull is promised when the
// prefix is undeclared or the record's content is already on disk.
func TestProfileNotFoundPullHint(t *testing.T) {
	defer remotesDirIn(t.TempDir())()
	cfg := Config{
		Profiles: map[string]Profile{"dev": {Env: map[string]string{"K": "v"}}},
		Remotes:  map[string]RemoteRecord{"other": {URL: "https://example.com/other.yaml"}},
	}
	_, err := cfg.ResolveProfile("other/base")
	if err == nil {
		t.Fatal("resolving a profile no layer defines must fail")
	}
	if got := err.Error(); !strings.Contains(got, `profile "other/base" not found`) || !strings.Contains(got, `enver pull other`) {
		t.Fatalf("err = %q, want the plain miss plus the pull that supplies it", got)
	}

	if err := WriteRemoteContent("other", []byte("profiles:\n  base:\n    env:\n      K: v\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.ResolveProfile("other/base"); err == nil || strings.Contains(err.Error(), "enver pull") {
		t.Fatalf("err = %v, want no pull hint once the record has content", err)
	}

	if _, err := cfg.ResolveProfile("nosuch/base"); err == nil || strings.Contains(err.Error(), "enver pull") {
		t.Fatalf("err = %v, want no pull hint for an undeclared prefix", err)
	}
}

// TestValidateUnsetLayerMatrix pins every layer × direction cell of the
// unset rules in one table: what the merged view reports, and what the
// isolated global file reports (enver validate runs both passes).
func TestValidateUnsetLayerMatrix(t *testing.T) {
	const k = "TOKEN"
	prof := func(env, unset bool) Profile {
		var p Profile
		if env {
			p.Env = map[string]string{k: "v"}
		}
		if unset {
			p.Unset = []string{k}
		}
		return p
	}
	cases := []struct {
		name         string
		gEnv, gUnset bool
		lEnv, lUnset bool
		wantMerged   []string
		wantIsolated []string
	}{
		{name: "local override of a live global key", gEnv: true, lEnv: true},
		{name: "local unset strips a global key (the feature)", gEnv: true, lUnset: true},
		{name: "local definition survives a global-layer unset (closest wins)", gUnset: true, lEnv: true},
		// Consumption at the fold applies a same-layer fence to its own copy, so
		// the merged view collapses to empty; validatecmd still surfaces the raw
		// contradiction through its ValidateGlobal pass.
		{name: "global defines and unsets its own key", gEnv: true, gUnset: true,
			wantMerged:   []string{"empty:"},
			wantIsolated: []string{"contradictory-unset:TOKEN"}},
		{name: "local defines and unsets its own key", lEnv: true, lUnset: true,
			wantMerged:   []string{"contradictory-unset:TOKEN"},
			wantIsolated: []string{"empty:"}},
		{name: "local unset launders a global contradiction in the merged view", gEnv: true, gUnset: true, lUnset: true,
			wantIsolated: []string{"contradictory-unset:TOKEN"}},
		{name: "local define and unset beside a global fence", gUnset: true, lEnv: true, lUnset: true,
			wantMerged: []string{"contradictory-unset:TOKEN"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			global := Config{Profiles: map[string]Profile{"dev": prof(tc.gEnv, tc.gUnset)}}
			local := Config{Profiles: map[string]Profile{"dev": prof(tc.lEnv, tc.lUnset)}}
			collect := func(cfg Config) []string {
				var got []string
				for _, is := range Validate(cfg) {
					got = append(got, string(is.Kind)+":"+is.Target)
				}
				return got
			}
			isolated := collect(global)
			check := func(got, want []string) {
				t.Helper()
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("issues = %v, want %v", got, want)
				}
			}
			check(collect(Merge(global, local, LayerGlobal, LayerLocal)), tc.wantMerged)
			check(isolated, tc.wantIsolated)
		})
	}
}

// TestValidateUnsetChainMatrix pins the extends-chain direction of the same
// rules inside one layer: a child unset stripping a parent key is the feature
// (silent), and a child redefinition overriding an ancestor's unset is the
// closest-wins feature too (silent). Only a same-profile define+unset pair is
// a contradiction.
func TestValidateUnsetChainMatrix(t *testing.T) {
	feature := Config{Profiles: map[string]Profile{
		"base":  {Env: map[string]string{"TOKEN": "v"}},
		"child": {Extends: Extends{"base"}, Unset: []string{"TOKEN"}},
	}}
	for _, is := range Validate(feature) {
		if is.Profile == "child" {
			t.Fatalf("child unsetting a parent key is the feature, reported as %+v", is)
		}
	}

	closest := Config{Profiles: map[string]Profile{
		"base":  {Unset: []string{"TOKEN"}},
		"child": {Extends: Extends{"base"}, Env: map[string]string{"TOKEN": "mine"}},
	}}
	for _, is := range Validate(closest) {
		if is.Profile == "child" {
			t.Fatalf("child redefinition overriding an ancestor unset is the feature, reported as %+v", is)
		}
	}
}
