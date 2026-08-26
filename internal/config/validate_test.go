package config

import (
	"os"
	"path/filepath"
	"testing"
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
	kinds := map[string]string{}
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
	got := map[string]string{}
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

func TestValidateGlobalIsolated(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.yaml")
	// broken extends dev, but dev is not defined in the global file.
	if err := os.WriteFile(globalPath, []byte("profiles:\n  broken:\n    extends: dev\n    env:\n      A: \"1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// ValidateGlobal loads the global file alone, so "dev" dangles and the issue
	// is attributed to the global scope.
	var found bool
	for _, is := range ValidateGlobal(globalPath) {
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
	for _, is := range Validate(Merge(global, local)) {
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
	if !hasIssue(Validate(Merge(global, localBoth)), "contradictory-unset") {
		t.Error("a local define+unset pair must stay a contradictory-unset warning")
	}
}

func hasIssue(issues []Issue, kind string) bool {
	for _, is := range issues {
		if is.Kind == kind {
			return true
		}
	}
	return false
}
