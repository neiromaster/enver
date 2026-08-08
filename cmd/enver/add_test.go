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
