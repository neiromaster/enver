package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenameProfileRewritesRefs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	in := `default: anth
profiles:
  anth:
    env:
      K: v
  local:
    extends: anth
    env:
      K2: v2
`
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenameProfile(path, "anth", "base"); err != nil {
		t.Fatalf("RenameProfile: %v", err)
	}
	s := string(mustRead(t, path))
	if !strings.Contains(s, "base:") || strings.Contains(s, "anth:") {
		t.Fatalf("profile key not renamed:\n%s", s)
	}
	if !strings.Contains(s, "extends: base") {
		t.Fatalf("extends reference not rewritten:\n%s", s)
	}
	if !strings.Contains(s, "default: base") {
		t.Fatalf("default pointer not rewritten:\n%s", s)
	}
}

func TestRenameProfileRefusesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  a:\n    env: {K: v}\n  b:\n    env: {K: v}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenameProfile(path, "a", "b"); err == nil {
		t.Fatal("rename onto existing name should fail")
	}
}

func TestRenameProfileAbsentSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  a:\n    env: {K: v}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenameProfile(path, "nope", "x"); err == nil {
		t.Fatal("absent source should fail")
	}
}

func TestSetAndClearDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  a:\n    env: {K: v}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDefault(path, "a"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if !strings.Contains(string(mustRead(t, path)), "default: a") {
		t.Fatal("default not set")
	}
	if err := ClearDefault(path); err != nil {
		t.Fatalf("ClearDefault: %v", err)
	}
	if strings.Contains(string(mustRead(t, path)), "default") {
		t.Fatal("default not cleared")
	}
}
