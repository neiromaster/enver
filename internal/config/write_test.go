package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteProfileReplacesEnvAndDeletesAbsentKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	in := `profiles:
  anth:
    env:
      KEEP: "1"
      DROP: "1"
      API_KEY: old
`
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	// New env keeps KEEP and adds MODEL; DROP and API_KEY are deleted. The
	// comments map is authoritative — WriteProfile writes exactly these keys
	// with exactly these comments (the caller, edit, passes the full set seeded
	// from ReadProfile).
	p := Profile{Env: map[string]string{"KEEP": "1", "MODEL": "claude-sonnet-5"}, Comments: map[string]string{"KEEP": "kept hint", "MODEL": "chosen model"}}
	if err := WriteProfile(path, "anth", p, false, false); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	s := string(mustRead(t, path))
	if strings.Contains(s, "DROP") || strings.Contains(s, "API_KEY") {
		t.Fatalf("absent keys not deleted:\n%s", s)
	}
	// !!str renders int-like values quoted (type-safe); plain strings stay unquoted.
	if !strings.Contains(s, `KEEP: "1"`) {
		t.Fatalf("KEEP missing or not quoted:\n%s", s)
	}
	if !strings.Contains(s, "MODEL: claude-sonnet-5") {
		t.Fatalf("MODEL missing:\n%s", s)
	}
	if !strings.Contains(s, "kept hint") || !strings.Contains(s, "chosen model") {
		t.Fatalf("comments from the map missing:\n%s", s)
	}
}

func TestWriteProfileClearsExtendsAndSetsDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  anth:\n    extends: base\n    env:\n      K: v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfile(path, "anth", Profile{Extends: nil, Env: map[string]string{"K": "v"}}, true, false); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	s := string(mustRead(t, path))
	if strings.Contains(s, "extends") {
		t.Fatalf("extends not cleared:\n%s", s)
	}
	if !strings.Contains(s, "default: anth") {
		t.Fatalf("default not set:\n%s", s)
	}
}

func TestWriteProfileClearsDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("default: anth\nprofiles:\n  anth:\n    env:\n      K: v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfile(path, "anth", Profile{Env: map[string]string{"K": "v"}}, false, true); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	if strings.Contains(string(mustRead(t, path)), "default") {
		t.Fatal("default not cleared")
	}
}

func TestWriteProfileCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.yaml")
	if err := WriteProfile(path, "anth", Profile{Env: map[string]string{"K": "v"}}, true, false); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(string(mustRead(t, path)), "anth") {
		t.Fatal("file not created with profile")
	}
}

func TestDeleteProfileRemovesNodeAndPreservesOthers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	in := `default: anth
profiles:
  anth:
    env:
      K: v
  glm:
    env:
      K2: v2
`
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeleteProfile(path, "glm"); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	s := string(mustRead(t, path))
	if strings.Contains(s, "glm") {
		t.Fatalf("profile not removed:\n%s", s)
	}
	if !strings.Contains(s, "anth") {
		t.Fatalf("sibling profile lost:\n%s", s)
	}
}

func TestDeleteProfileMissingIsNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absent.yaml")
	if err := DeleteProfile(path, "anth"); err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
}

// TestDeleteProfileClearsDefault: removing the profile that is the file's
// default also clears the default key, leaving a valid no-default config.
func TestDeleteProfileClearsDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	in := `default: anth
profiles:
  anth:
    env:
      K: v
  glm:
    env:
      K2: v2
`
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeleteProfile(path, "anth"); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	s := string(mustRead(t, path))
	if strings.Contains(s, "anth") {
		t.Fatalf("profile not removed:\n%s", s)
	}
	if strings.Contains(s, "default") {
		t.Fatalf("default key not cleared:\n%s", s)
	}
	if !strings.Contains(s, "glm") {
		t.Fatalf("sibling profile lost:\n%s", s)
	}
}

// TestDeleteProfilePreservesUnrelatedDefault: removing a non-default profile
// leaves the default key pointing at another profile untouched.
func TestDeleteProfilePreservesUnrelatedDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	in := `default: glm
profiles:
  anth:
    env:
      K: v
  glm:
    env:
      K2: v2
`
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeleteProfile(path, "anth"); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	s := string(mustRead(t, path))
	if strings.Contains(s, "anth") {
		t.Fatalf("profile not removed:\n%s", s)
	}
	if !strings.Contains(s, "default: glm") {
		t.Fatalf("unrelated default lost:\n%s", s)
	}
}
