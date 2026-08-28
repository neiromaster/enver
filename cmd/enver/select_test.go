package main

import (
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
)

func TestProfileOptionsAnnotatesExtends(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"anth":  {},
		"local": {Extends: config.Extends{"anth"}},
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

func TestSeedOptionsAppendsUnmatched(t *testing.T) {
	opts := []ui.Option{
		{Value: "base", Label: "base"},
		{Value: "ci", Label: "ci (extends → base)"},
	}
	got := seedOptions(config.Extends{"ci", "ghost", "staging"}, opts)
	if len(got) != 2 {
		t.Fatalf("got %d external options, want 2: %+v", len(got), got)
	}
	for i, want := range []string{"ghost", "staging"} {
		if got[i].Value != want {
			t.Fatalf("got[%d] = %q, want %q (seed order kept)", i, got[i].Value, want)
		}
		if got[i].Label != want+" (external)" || !got[i].Dim {
			t.Fatalf("got[%d] = %+v, want dimmed %q (external) row", i, got[i], want)
		}
	}
}

func TestSeedOptionsAllMatchedAddsNothing(t *testing.T) {
	opts := []ui.Option{{Value: "base", Label: "base"}}
	if got := seedOptions(config.Extends{"base"}, opts); len(got) != 0 {
		t.Fatalf("got %+v, want no extras when every seed is offered", got)
	}
}
