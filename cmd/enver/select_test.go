package main

import (
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

func TestProfileOptionsAnnotatesExtends(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"anth":  {},
		"local": {Extends: "anth"},
	}}
	opts := profileOptions(cfg, "")
	if len(opts) != 2 {
		t.Fatalf("got %d options, want 2", len(opts))
	}
	want := map[string]string{"anth": "anth", "local": "local (extends → anth)"}
	for _, o := range opts {
		if want[o.Value] != o.Label {
			t.Errorf("option %q label = %q, want %q", o.Value, o.Label, want[o.Value])
		}
	}
}

func TestProfileOptionsExcludes(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{"a": {}, "b": {}}}
	opts := profileOptions(cfg, "a")
	for _, o := range opts {
		if o.Value == "a" {
			t.Fatal("excluded profile still present")
		}
	}
}
