package config

import "testing"

func TestValidateFindsDanglingAndCycle(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"ok":     {Extends: "base"},
		"base":   {Env: map[string]string{"K": "v"}},
		"dangle": {Extends: "ghost"},
		"a":      {Extends: "b"},
		"b":      {Extends: "a"},
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
	cfg := Config{Profiles: map[string]Profile{"x": {Extends: "ghost"}}}
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
		"a": {Extends: "b"},
		"b": {Extends: "ghost"},
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
