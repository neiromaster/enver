package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemotesRecordMerge(t *testing.T) {
	base := Config{Remotes: map[string]RemoteRecord{
		"llm":    {URL: "github:someone/llm-profiles", Ref: "v1"},
		"legacy": {URL: "https://example.com/old.yaml"},
	}}
	over := Config{Remotes: map[string]RemoteRecord{
		"llm": {URL: "https://example.com/llm.yaml", Ref: "v2"},
	}}
	got := Merge(base, over, LayerGlobal, LayerLocal)
	if r := got.Remotes["llm"]; r != (RemoteRecord{URL: "https://example.com/llm.yaml", Ref: "v2"}) {
		t.Fatalf("merged llm = %+v, want the local record whole", r)
	}
	if r := got.Remotes["legacy"]; r != (RemoteRecord{URL: "https://example.com/old.yaml"}) {
		t.Fatalf("merged legacy = %+v, want the global-only record kept", r)
	}
	if base.Remotes["llm"].Ref != "v1" {
		t.Fatalf("base remotes mutated: %+v", base.Remotes)
	}
	got.Remotes["legacy"] = RemoteRecord{URL: "https://example.com/new.yaml"}
	if base.Remotes["legacy"].URL != "https://example.com/old.yaml" {
		t.Fatal("merged remotes map aliases the base map")
	}

	// The record is the unit: an overriding record with no ref clears the
	// inherited ref rather than inheriting it.
	partial := Merge(base, Config{Remotes: map[string]RemoteRecord{
		"llm": {URL: "https://example.com/llm.yaml"},
	}}, LayerGlobal, LayerLocal)
	if r := partial.Remotes["llm"]; r != (RemoteRecord{URL: "https://example.com/llm.yaml"}) {
		t.Fatalf("merged llm = %+v, want no inherited ref", r)
	}

	if r := Merge(Config{}, Config{}, LayerGlobal, LayerLocal).Remotes; r != nil {
		t.Fatalf("remotes = %v, want nil when no layer declares any", r)
	}
}

func TestRemotesYAMLParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "remotes:\n" +
		"  llm:\n" +
		"    url: github:someone/llm-profiles\n" +
		"    ref: v2\n" +
		"  plain:\n" +
		"    url: https://example.com/profiles.yaml\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if r := cfg.Remotes["llm"]; r != (RemoteRecord{URL: "github:someone/llm-profiles", Ref: "v2"}) {
		t.Fatalf("llm = %+v, want url and ref", r)
	}
	if r := cfg.Remotes["plain"]; r != (RemoteRecord{URL: "https://example.com/profiles.yaml"}) {
		t.Fatalf("plain = %+v, want url with an empty ref", r)
	}
}

func TestRemotesInvalidRecordName(t *testing.T) {
	for _, name := range []string{"llm", "llm-2", "LLM_1", "0"} {
		if !ValidRecordName(name) {
			t.Errorf("ValidRecordName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "a/b", "/x", "llm/", "a b", "-x", "llm.profiles"} {
		if ValidRecordName(name) {
			t.Errorf("ValidRecordName(%q) = true, want false", name)
		}
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "remotes:\n  a/b:\n    url: https://example.com/x.yaml\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("load must reject a record name the prefix rule forbids")
	}
	if !strings.Contains(err.Error(), `"a/b"`) {
		t.Fatalf("err = %v, want it to name the record", err)
	}
}
