package config

import (
	"os"
	"path/filepath"
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
	if err := UpsertProfile(path, "anth", p, false); err != nil {
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
	p := Profile{Extends: "anth", Env: map[string]string{"K": "v"}}
	if err := UpsertProfile(path, "new", p, true); err != nil {
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

	n, err := EncryptFile(path, key, "", false)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if n != 1 {
		t.Fatalf("encrypted %d values, want 1 (only API_KEY is secret-looking)", n)
	}
	enc, _ := os.ReadFile(path)
	if !strings.Contains(string(enc), "enc:v1:") {
		t.Fatal("encrypted value not found in file")
	}
	if !strings.Contains(string(enc), "claude-sonnet-5") {
		t.Fatal("non-secret value got encrypted")
	}

	// idempotent
	n2, err := EncryptFile(path, key, "", false)
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