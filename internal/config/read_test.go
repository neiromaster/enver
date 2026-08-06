// internal/config/read_test.go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadProfileValuesAndComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	in := `default: anth
profiles:
  anth:
    extends: base
    env:
      # token from the vault
      API_KEY: sk-xxx
      MODEL: claude-sonnet-5
`
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	p, comments, isDefault, ok, err := ReadProfile(path, "anth")
	if err != nil {
		t.Fatalf("ReadProfile: %v", err)
	}
	if !ok {
		t.Fatal("ok = false for existing profile")
	}
	if !isDefault {
		t.Fatal("isDefault = false, want true")
	}
	if p.Extends != "base" {
		t.Fatalf("extends = %q, want base", p.Extends)
	}
	if p.Env["API_KEY"] != "sk-xxx" || p.Env["MODEL"] != "claude-sonnet-5" {
		t.Fatalf("env = %v", p.Env)
	}
	if comments["API_KEY"] != "token from the vault" {
		t.Fatalf("comment = %q, want the vault hint", comments["API_KEY"])
	}
	if _, has := comments["MODEL"]; has {
		t.Fatal("empty comment should not be recorded")
	}
}

func TestReadProfileMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  anth:\n    env:\n      K: v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, ok, err := ReadProfile(path, "nope")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("ok = true for missing profile")
	}
}

func TestReadProfileMissingFile(t *testing.T) {
	_, _, _, ok, err := ReadProfile(filepath.Join(t.TempDir(), "absent.yaml"), "anth")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if ok {
		t.Fatal("ok = true for missing file")
	}
}
