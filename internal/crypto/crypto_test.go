package crypto

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := make([]byte, keySize)
	for i := range key {
		key[i] = byte(i)
	}
	salt := make([]byte, saltSize)
	cases := []string{
		"sk-ant-secret-1234567890",
		"",
		"short",
		strings.Repeat("x", 1000),
		"unicode: ключ-пароль-密码 🔐",
	}
	for _, plain := range cases {
		enc, err := EncryptValue(plain, key, salt)
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
	salt := make([]byte, saltSize)
	plain := "sk-same-input"
	a, _ := EncryptValue(plain, key, salt)
	b, _ := EncryptValue(plain, key, salt)
	if a == b {
		t.Fatal("two encryptions of the same value produced identical ciphertext (nonce not random)")
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	keyA := make([]byte, keySize)
	keyB := make([]byte, keySize)
	keyB[0] = 1
	enc, _ := EncryptValue("secret", keyA, make([]byte, saltSize))
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
	if IsEncrypted("enc:v1:YWJjZA==") {
		t.Fatal("enc:v1: is a dropped format and must not be reported as encrypted")
	}
	if !IsEncrypted("enc:v2:" + base64.StdEncoding.EncodeToString(make([]byte, 44))) {
		t.Fatal("enc:v2: value must be recognized as encrypted")
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
	// Windows has no Unix permission bits; os.WriteFile's mode is not preserved.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("key file perm = %o, want 0600", perm)
		}
	}
}

func TestLoadKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	if err := GenerateKey(path, false); err != nil {
		t.Fatal(err)
	}
	loaded, salt, err := LoadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != keySize {
		t.Fatalf("loaded key len = %d, want %d", len(loaded), keySize)
	}
	if len(salt) != saltSize {
		t.Fatalf("loaded salt len = %d, want %d", len(salt), saltSize)
	}
	enc, err := EncryptValue("secret", loaded, salt)
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

func TestDeriveKeyDeterministic(t *testing.T) {
	salt := make([]byte, 16)
	key1, err := DeriveKey("hunter2", salt)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	key2, err := DeriveKey("hunter2", salt)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if !bytes.Equal(key1, key2) {
		t.Fatal("same passphrase+salt must derive the same key")
	}
	otherSalt := make([]byte, 16)
	otherSalt[0] = 1
	key3, _ := DeriveKey("hunter2", otherSalt)
	if bytes.Equal(key1, key3) {
		t.Fatal("different salt must derive a different key")
	}
	key4, _ := DeriveKey("hunter3", salt)
	if bytes.Equal(key1, key4) {
		t.Fatal("different passphrase must derive a different key")
	}
	if len(key1) != keySize {
		t.Fatalf("key length = %d, want %d", len(key1), keySize)
	}
}

func TestEncryptV2RoundTrip(t *testing.T) {
	salt := make([]byte, 16)
	key := make([]byte, keySize)
	enc, err := EncryptValue("secret", key, salt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(enc, prefixV2) {
		t.Fatalf("encrypted value = %q, want enc:v2: prefix", enc)
	}
	plain, err := DecryptValue(enc, key)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plain != "secret" {
		t.Fatalf("plain = %q, want secret", plain)
	}
}

func TestSaltFromValue(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := make([]byte, keySize)
	enc, err := EncryptValue("secret", key, salt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	got, err := SaltFromValue(enc)
	if err != nil {
		t.Fatalf("salt from value: %v", err)
	}
	if !bytes.Equal(got, salt) {
		t.Fatalf("salt = %x, want %x", got, salt)
	}
	if _, err := SaltFromValue("enc:v1:AAAA"); err == nil {
		t.Fatal("SaltFromValue on a v1 value must error")
	}
	if _, err := SaltFromValue("plain"); err == nil {
		t.Fatal("SaltFromValue on plaintext must error")
	}
}

func TestEncryptValueRejectsBadSaltLength(t *testing.T) {
	key := make([]byte, keySize)
	badSalts := [][]byte{
		make([]byte, 20), // too long
		{},               // non-nil but empty
		nil,
	}
	for _, salt := range badSalts {
		if _, err := EncryptValue("secret", key, salt); err == nil {
			t.Fatalf("EncryptValue with %d-byte salt must error", len(salt))
		}
	}
}

func TestKeyCacheRejectsInvalid(t *testing.T) {
	valid := NewKeyCache([]byte("0123456789abcdef"), make([]byte, keySize))
	wrongKey := valid
	wrongKey.Key = make([]byte, 16)
	wrongSalt := valid
	wrongSalt.Salt = make([]byte, 8)

	cases := []struct {
		name string
		data []byte
	}{
		{"empty JSON object", []byte("{}")},
		{"truncated JSON", []byte(`{"v":1`)},
		{"wrong-length key", mustMarshalJSON(t, wrongKey)},
		{"wrong-length salt", mustMarshalJSON(t, wrongSalt)},
	}
	for _, tc := range cases {
		t.Run("parse "+tc.name, func(t *testing.T) {
			if _, err := parseKeyCache(tc.data); err == nil {
				t.Fatal("parseKeyCache accepted invalid cache data")
			}
		})
		t.Run("load "+tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			key, salt, err := LoadKey(path)
			if err == nil {
				t.Fatalf("LoadKey returned (%d-byte key, %d-byte salt, nil), want an error", len(key), len(salt))
			}
		})
	}

	// A well-formed cache must still load.
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, mustMarshalJSON(t, valid), 0o600); err != nil {
		t.Fatal(err)
	}
	key, salt, err := LoadKey(path)
	if err != nil {
		t.Fatalf("valid cache: %v", err)
	}
	if !bytes.Equal(key, valid.Key) || !bytes.Equal(salt, valid.Salt) {
		t.Fatal("valid cache round-trip mismatch")
	}
}

func mustMarshalJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestKeyCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "key")
	salt := []byte("0123456789abcdef")
	key := make([]byte, keySize)
	c := NewKeyCache(salt, key)
	if err := WriteKeyCache(path, c); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	got, err := LoadKeyCache(path)
	if err != nil {
		t.Fatalf("load cache: %v", err)
	}
	if !bytes.Equal(got.Salt, salt) || !bytes.Equal(got.Key, key) {
		t.Fatalf("cache round-trip mismatch: %+v", got)
	}
	if got.KDF != "argon2id" || got.Time != 3 || got.Memory != 64*1024 || got.Threads != 4 {
		t.Fatalf("cache params mismatch: %+v", got)
	}
}

func TestLoadKeyRejectsRawKeyFile(t *testing.T) {
	// Raw base64 key files were the enc:v1 era format; only JSON caches load now.
	rawPath := filepath.Join(t.TempDir(), "raw")
	key := make([]byte, keySize)
	if err := os.WriteFile(rawPath, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatalf("write raw: %v", err)
	}
	if _, _, err := LoadKey(rawPath); err == nil {
		t.Fatal("raw base64 key file must not load; want an invalid key cache error")
	}
}
