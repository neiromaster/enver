package crypto

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, keySize)
	for i := range key {
		key[i] = byte(i)
	}
	cases := []string{
		"sk-ant-secret-1234567890",
		"",
		"short",
		strings.Repeat("x", 1000),
		"unicode: ключ-пароль-密码 🔐",
	}
	for _, plain := range cases {
		enc, err := EncryptValue(plain, key)
		if err != nil {
			t.Fatalf("encrypt %q: %v", plain, err)
		}
		if !IsEncrypted(enc) {
			t.Fatalf("encrypted value missing prefix: %q", enc)
		}
		got, err := DecryptValue(enc, key)
		if err != nil {
			t.Fatalf("decrypt %q: %v", plain, err)
		}
		if got != plain {
			t.Fatalf("round-trip mismatch: got %q want %q", got, plain)
		}
	}
}

func TestEncryptUsesRandomNonce(t *testing.T) {
	key := make([]byte, keySize)
	plain := "sk-same-input"
	a, _ := EncryptValue(plain, key)
	b, _ := EncryptValue(plain, key)
	if a == b {
		t.Fatal("two encryptions of the same value produced identical ciphertext (nonce not random)")
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	keyA := make([]byte, keySize)
	keyB := make([]byte, keySize)
	keyB[0] = 1
	enc, _ := EncryptValue("secret", keyA)
	if _, err := DecryptValue(enc, keyB); err == nil {
		t.Fatal("decrypt with wrong key should fail (GCM auth)")
	}
}

func TestDecryptNonEncryptedFails(t *testing.T) {
	key := make([]byte, keySize)
	if _, err := DecryptValue("sk-plain", key); err == nil {
		t.Fatal("decrypting a non-encrypted value should error")
	}
}

func TestIsEncrypted(t *testing.T) {
	if IsEncrypted("sk-ant-xxx") {
		t.Fatal("plain value reported as encrypted")
	}
	if !IsEncrypted("enc:v1:YWJjZA==") {
		t.Fatal("enc:v1: value not reported as encrypted")
	}
}

func TestGenerateKeyRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := GenerateKey(path, false); err != nil {
		t.Fatalf("first keygen: %v", err)
	}
	if err := GenerateKey(path, false); err == nil {
		t.Fatal("second keygen without --force should fail")
	}
	if err := GenerateKey(path, true); err != nil {
		t.Fatalf("keygen --force: %v", err)
	}
}

func TestGenerateKeyPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "key")
	if err := GenerateKey(path, false); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file perm = %o, want 0600", perm)
	}
}

func TestLoadKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := GenerateKey(path, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != keySize {
		t.Fatalf("loaded key len = %d, want %d", len(loaded), keySize)
	}
	enc, err := EncryptValue("secret", loaded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptValue(enc, loaded); err != nil {
		t.Fatalf("round-trip via loaded key: %v", err)
	}
}

func TestDecodeKeyInvalid(t *testing.T) {
	if _, err := DecodeKey("!!!not-base64!!!"); err == nil {
		t.Fatal("invalid base64 should error")
	}
	if _, err := DecodeKey("dG9v"); err == nil { // too short
		t.Fatal("wrong-length key should error")
	}
}