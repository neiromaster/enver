package main

import (
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
