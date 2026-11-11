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
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("DeleteProfile must not create the file")
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

func TestWriteProfileNonNilMarkedClearsMark(t *testing.T) {
	// A non-nil Marked map is authoritative: an empty one says the key is not a
	// secret, so the mark the file carried must go.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      ~K: v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfile(path, "p", Profile{Env: map[string]string{"K": "v"}, Marked: map[string]bool{}}, false, false); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("reload after write: %v", err)
	}
	if got := cfg.Profiles["p"]; got.Env["K"] != "v" || got.Marked != nil {
		t.Fatalf("entry = env %#v marked %#v, want K unmarked", got.Env, got.Marked)
	}
}

func TestWriteProfileNilMarkedPreserves(t *testing.T) {
	// No mark information: the mark already on the entry survives the rewrite.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      ~K: v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfile(path, "p", Profile{Env: map[string]string{"K": "new"}}, false, false); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("reload after write: %v", err)
	}
	got := cfg.Profiles["p"]
	if got.Env["K"] != "new" {
		t.Fatalf("env = %#v, want K: new", got.Env)
	}
	if !got.Marked["K"] {
		t.Fatalf("mark dropped on rewrite: env=%#v marked=%#v", got.Env, got.Marked)
	}
}

func TestWriteProfileAuthoritativeMarkedSetsMark(t *testing.T) {
	// A non-nil Marked map also marks a key the file holds plain: the caller
	// knows better than the file it is about to rewrite.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      K: v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfile(path, "p", Profile{Env: map[string]string{"K": "v"}, Marked: map[string]bool{"K": true}}, false, false); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("reload after write: %v", err)
	}
	if got := cfg.Profiles["p"]; !got.Marked["K"] {
		t.Fatalf("mark not written: env=%#v marked=%#v", got.Env, got.Marked)
	}
}

func TestWriteProfileNilMarkedFallsBackToChainInFile(t *testing.T) {
	// The mark lives on an ancestor in the same file, so a wholesale rewrite
	// that keeps the extends must keep the inherited mark too.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "profiles:\n  base:\n    env:\n      ~K: v\n  work:\n    extends: base\n    env: {}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfile(path, "work", Profile{Extends: Extends{"base"}, Env: map[string]string{"K": "y"}}, false, false); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("reload after write: %v", err)
	}
	work := cfg.Profiles["work"]
	if work.Env["K"] != "y" || !work.Marked["K"] {
		t.Fatalf("entry = env %#v marked %#v, want K: y marked", work.Env, work.Marked)
	}
}

func TestWriteProfileClearedExtendsKeepsInheritedMark(t *testing.T) {
	// The mark came from the ancestor this write removes. The resolver has to
	// read the chain as it stood before the rewrite, or the mark vanishes with
	// the ancestor that carried it.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "profiles:\n  base:\n    env:\n      ~K: v\n  work:\n    extends: base\n    env: {}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfile(path, "work", Profile{Env: map[string]string{"K": "y"}}, false, false); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("reload after write: %v", err)
	}
	work := cfg.Profiles["work"]
	if len(work.Extends) != 0 {
		t.Fatalf("extends = %v, want cleared", work.Extends)
	}
	if work.Env["K"] != "y" || !work.Marked["K"] {
		t.Fatalf("entry = env %#v marked %#v, want the inherited mark kept", work.Env, work.Marked)
	}
}

func TestWriteProfileNewExtendsMakesInheritedMarkVisible(t *testing.T) {
	// The mirror case: the ancestor arrives with this write, so only the chain
	// as it stands afterwards can tell the writer about the mark.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "profiles:\n  base:\n    env:\n      ~K: v\n  work:\n    env: {}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteProfile(path, "work", Profile{Extends: Extends{"base"}, Env: map[string]string{"K": "y"}}, false, false); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("reload after write: %v", err)
	}
	work := cfg.Profiles["work"]
	if !work.Extends.Has("base") {
		t.Fatalf("extends = %v, want base", work.Extends)
	}
	if work.Env["K"] != "y" || !work.Marked["K"] {
		t.Fatalf("entry = env %#v marked %#v, want the newly inherited mark", work.Env, work.Marked)
	}
}

func TestWriteProfileRejectsInvalidNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteProfile(path, "p", Profile{Env: map[string]string{"OK": "1"}}, false, false); err != nil {
		t.Fatalf("valid names must write: %v", err)
	}
	if err := WriteProfile(path, "p", Profile{Unset: []string{"K; x"}}, false, false); err == nil {
		t.Error("WriteProfile must reject an invalid unset entry")
	}
	if err := UpsertProfile(path, "p", Profile{Env: map[string]string{"BAD-KEY": "1"}}, false, false); err == nil {
		t.Error("UpsertProfile must reject an invalid env key")
	}
	if _, err := LoadFile(path); err != nil {
		t.Fatalf("the earlier valid write must have left a loadable file: %v", err)
	}
}

func TestWriteProfileRejectsNonMappingRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "- a\n- b\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteProfile(path, "p", Profile{Env: map[string]string{"K": "v"}}, false, false)
	if err == nil || !strings.Contains(err.Error(), "not a mapping") {
		t.Fatalf("WriteProfile on sequence root: err=%v, want not-a-mapping error", err)
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(data) != body {
		t.Fatalf("file was modified: %q", data)
	}
}

func TestDeleteProfileBoolSpelledName(t *testing.T) {
	// A profile named `true` decodes its mapping key with a !!bool tag; the
	// delete path must remove it, not report success while leaving it behind.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "profiles:\n  true:\n    env:\n      K: v\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := DeleteProfile(path, "true"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("reload after delete: %v", err)
	}
	if _, ok := cfg.Profiles["true"]; ok {
		t.Fatal("profile `true` survived deletion")
	}
}
