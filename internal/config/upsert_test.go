package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/crypto"
)

func TestUpsertPreservesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `# top-level comment
default: anth

profiles:
  # endpoint for anthropic
  anth:
    env:
      ANTHROPIC_API_KEY: sk-ant-xxx
      ANTHROPIC_MODEL: claude-sonnet-5
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Profile{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com"}}
	if err := UpsertProfile(path, "anth", p, false, false, nil); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "# top-level comment") {
		t.Error("top-level comment lost")
	}
	if !strings.Contains(s, "# endpoint for anthropic") {
		t.Error("inline profile comment lost")
	}
	if !strings.Contains(s, "ANTHROPIC_BASE_URL: https://api.anthropic.com") {
		t.Error("new env key not written")
	}
	if !strings.Contains(s, "ANTHROPIC_API_KEY: sk-ant-xxx") {
		t.Error("existing env key lost")
	}
}

func TestUpsertCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.yaml")
	p := Profile{Extends: Extends{"anth"}, Env: map[string]string{"K": "v"}}
	if err := UpsertProfile(path, "new", p, true, false, nil); err != nil {
		t.Fatalf("upsert into missing file: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "new:") || !strings.Contains(s, "extends: anth") {
		t.Fatalf("created file missing expected content:\n%s", s)
	}
	if !strings.Contains(s, "default: new") {
		t.Fatalf("default not set when requested:\n%s", s)
	}
}

func TestEncryptDecryptFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `default: anth
profiles:
  anth:
    env:
      ANTHROPIC_API_KEY: sk-ant-secret-1234567890
      ANTHROPIC_MODEL: claude-sonnet-5
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	salt := []byte("0123456789abcdef")
	n, err := EncryptFile(path, key, "", false, salt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if n != 1 {
		t.Fatalf("encrypted %d values, want 1 (only API_KEY is secret-looking)", n)
	}
	enc, _ := os.ReadFile(path)
	if !strings.Contains(string(enc), "enc:v2:") {
		t.Fatal("encrypted value not found in file or lacks enc:v2: prefix")
	}
	if !strings.Contains(string(enc), "claude-sonnet-5") {
		t.Fatal("non-secret value got encrypted")
	}

	// idempotent
	n2, err := EncryptFile(path, key, "", false, salt)
	if err != nil {
		t.Fatalf("re-encrypt: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("re-encrypt encrypted %d, want 0 (idempotent)", n2)
	}

	// decrypt restores plaintext
	n3, err := DecryptFile(path, key, "")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if n3 != 1 {
		t.Fatalf("decrypted %d, want 1", n3)
	}
	dec, _ := os.ReadFile(path)
	if !strings.Contains(string(dec), "sk-ant-secret-1234567890") {
		t.Fatal("decrypted plaintext not restored")
	}
}

func TestEncryptFileWrongKeyFailsDecrypt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      API_KEY: secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	keyB[0] = 1
	if _, err := EncryptFile(path, keyA, "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptFile(path, keyB, ""); err == nil {
		t.Fatal("decrypt with wrong key should fail")
	}
}

func TestCryptoPrefixMatch(t *testing.T) {
	if !crypto.IsEncrypted("enc:v1:YWJj") {
		t.Fatal("IsEncrypted should match enc:v1:")
	}
}

func TestUpsertWritesEnvCommentAboveEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	p := Profile{Env: map[string]string{"API_KEY": "sk-xxx"}}
	comments := map[string]string{"API_KEY": "get this token from vault X"}
	if err := UpsertProfile(path, "anth", p, false, false, comments); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	s := string(mustRead(t, path))
	// Comment renders, indented, on its own line immediately above the entry,
	// regardless of yaml.v3's indent width or key-vs-value attachment.
	re := regexp.MustCompile(`(?m)^[ \t]*# get this token from vault X[ \t]*\n[ \t]*API_KEY:`)
	if !re.MatchString(s) {
		t.Fatalf("comment not rendered above API_KEY entry:\n%s", s)
	}
}

func TestUpsertKeepsCommentWhenValueUpdatedWithoutComment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := UpsertProfile(path, "anth",
		Profile{Env: map[string]string{"API_KEY": "v1"}},
		false, false, map[string]string{"API_KEY": "from vault"}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	// Re-upsert the same key with a new value but no comment: value changes,
	// the existing comment must survive.
	if err := UpsertProfile(path, "anth",
		Profile{Env: map[string]string{"API_KEY": "v2"}},
		false, false, nil); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	s := string(mustRead(t, path))
	if !strings.Contains(s, "API_KEY: v2") {
		t.Fatalf("value not updated:\n%s", s)
	}
	if !strings.Contains(s, "from vault") {
		t.Fatalf("existing comment wiped on re-upsert:\n%s", s)
	}
}

func TestEnvCommentSurvivesEncryptDecrypt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := UpsertProfile(path, "anth",
		Profile{Env: map[string]string{"API_KEY": "sk-secret"}},
		false, false, map[string]string{"API_KEY": "from vault"}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	if _, err := EncryptFile(path, key, "", false); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	enc := string(mustRead(t, path))
	if !strings.Contains(enc, "from vault") {
		t.Fatalf("comment lost after encrypt:\n%s", enc)
	}
	if _, err := DecryptFile(path, key, ""); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	dec := string(mustRead(t, path))
	if !strings.Contains(dec, "from vault") {
		t.Fatalf("comment lost after decrypt:\n%s", dec)
	}
}

func TestUpsertForceExtendsClearsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := UpsertProfile(path, "p", Profile{Extends: Extends{"base"}, Env: map[string]string{"A": "1"}}, false, false, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := UpsertProfile(path, "p", Profile{Env: map[string]string{"B": "2"}}, false, true, nil); err != nil {
		t.Fatalf("forceExtends clear: %v", err)
	}
	prof, _, _, _, err := ReadProfile(path, "p")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(prof.Extends) > 0 {
		t.Errorf("Extends = %q, want empty (forceExtends cleared it)", prof.Extends)
	}
	if prof.Env["A"] != "1" || prof.Env["B"] != "2" {
		t.Errorf("env should merge to {A:1, B:2}, got %+v", prof.Env)
	}
}

func TestUpsertPreserveExtendsKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := UpsertProfile(path, "p", Profile{Extends: Extends{"base"}, Env: map[string]string{"A": "1"}}, false, false, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := UpsertProfile(path, "p", Profile{Env: map[string]string{"B": "2"}}, false, false, nil); err != nil {
		t.Fatalf("preserve: %v", err)
	}
	prof, _, _, _, err := ReadProfile(path, "p")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !prof.Extends.Has("base") {
		t.Errorf("Extends = %q, want base (forceExtends=false preserves it)", prof.Extends)
	}
	if prof.Env["A"] != "1" || prof.Env["B"] != "2" {
		t.Errorf("env should merge to {A:1, B:2}, got %+v", prof.Env)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFirstEncryptedValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	key := make([]byte, 32)
	salt := []byte("0123456789abcdef")
	enc, err := crypto.EncryptValue("secret", key, salt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// No encrypted values.
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      A: \"1\"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := FirstEncryptedValue(path)
	if err != nil || got != "" {
		t.Fatalf("plain config: got %q err=%v, want empty", got, err)
	}
	// One v2 value.
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      A: \""+enc+"\"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err = FirstEncryptedValue(path)
	if err != nil || got != enc {
		t.Fatalf("v2 config: got %q err=%v, want %q", got, err, enc)
	}
	// Missing file → error.
	if _, err := FirstEncryptedValue(filepath.Join(dir, "nope.yaml")); err == nil {
		t.Fatal("missing file must error")
	}
}
