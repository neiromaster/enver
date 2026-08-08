package main

import (
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/ui"
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
	prof, comments := buildProfile("anth", entries)
	if prof.Extends != "anth" {
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

func TestMaskedEntries(t *testing.T) {
	src := []ui.EnvEntry{
		{Key: "API_TOKEN", Value: "sk-abcdefghijklmnopqrstuvwxyz", Comment: "vault"},
		{Key: "PORT", Value: "5432"},
	}
	got := maskedEntries(src)

	if got[0].Value == src[0].Value {
		t.Fatal("secret value must be masked")
	}
	if !strings.HasPrefix(got[0].Value, "sk-a") {
		t.Fatalf("masked value should start with first 4 chars: %q", got[0].Value)
	}
	if got[0].Comment != "vault" {
		t.Fatalf("comment must pass through: %q", got[0].Comment)
	}
	if got[1].Value != "5432" {
		t.Fatalf("non-secret value must be unchanged: %q", got[1].Value)
	}
	if src[0].Value != "sk-abcdefghijklmnopqrstuvwxyz" {
		t.Fatal("maskedEntries must not mutate its input slice")
	}
}

func TestBuildSummary(t *testing.T) {
	entries := []ui.EnvEntry{
		{Key: "PORT", Value: "5432"},
		{Key: "API_TOKEN", Value: "sk-abcdefghijklmnopqrstuvwxyz"},
	}
	parent := map[string]string{"API_TOKEN": "sk-old", "LOG_LEVEL": "info"}
	got := buildSummary(entries, parent)

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
