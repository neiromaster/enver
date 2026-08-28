package main

import (
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
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

func TestParentEnvForResolvesChain(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Env: map[string]string{"A": "1"}},
		"ci":   {Env: map[string]string{"A": "2", "B": "3"}},
		"dev":  {Env: map[string]string{"A": "own", "C": "own"}, Unset: config.Unsets{"A"}},
	}}
	got, warns := parentEnvFor(cfg, "dev", config.Extends{"base", "ci"})
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
	got, warns := parentEnvFor(cfg, "dev", nil)
	if len(got) != 0 || len(warns) != 0 {
		t.Fatalf("parentEnv = %v, warns = %v, want empty", got, warns)
	}
}

func TestParentEnvForCycleIsEmpty(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"base": {Extends: config.Extends{"dev"}, Env: map[string]string{"A": "1"}},
		"dev":  {},
	}}
	got, warns := parentEnvFor(cfg, "dev", config.Extends{"base"})
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
	got, warns := parentEnvFor(cfg, "dev", config.Extends{"base", "ci"})
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

// TestParentEnvForResolvesThroughMergedParents pins the view rule: the
// backdrop resolves parents from the config it is given, and doAdd passes the
// merged view — the same one the cycle check uses — so a parent the cycle
// check resolved never comes back as hidden.
func TestParentEnvForResolvesThroughMergedParents(t *testing.T) {
	merged := config.Config{Profiles: map[string]config.Profile{
		"child": {Extends: config.Extends{"base"}},
		"base":  {Env: map[string]string{"B": "1"}},
	}}
	env, warns := parentEnvFor(merged, "newkid", config.Extends{"child"})
	if len(warns) != 0 {
		t.Fatalf("unexpected warns: %v", warns)
	}
	if env["B"] != "1" {
		t.Fatalf("env=%v, want inherited B=1", env)
	}
}
