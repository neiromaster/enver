package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/crypto"
)

// writeMixedSaltConfig writes a config whose profile q holds a value encrypted
// under (keyT, saltT) while p is plaintext: the stranded-era layout the
// profile-scoped salt guard must let coexist with encrypting p.
func writeMixedSaltConfig(t *testing.T, keyT, saltT []byte) (path, encT string) {
	t.Helper()
	encT, err := crypto.EncryptValueWithParams("old", keyT, saltT, crypto.CurrentParams)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "config.yaml")
	cfg := "profiles:\n  q:\n    env:\n      OLD: " + encT + "\n  p:\n    env:\n      TOKEN: plain\n"
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return path, encT
}

func TestEncryptFileSaltGuardScopedToWrittenProfiles(t *testing.T) {
	keyT := make([]byte, 32)
	keyA := make([]byte, 32)
	keyA[0] = 1
	saltT := []byte("aaaaaaaaaaaaaaaa")
	saltA := []byte("bbbbbbbbbbbbbbbb")

	// Encrypting the plaintext profile beside a stranded one succeeds.
	path, encT := writeMixedSaltConfig(t, keyT, saltT)
	n, err := EncryptFile(path, keyA, saltA, "p", false)
	if err != nil || n != 1 {
		t.Fatalf("scoped encrypt: n=%d err=%v, want 1/nil", n, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), encT) {
		t.Fatal("stranded value in the unwritten profile must be left untouched")
	}
	dn, err := DecryptFile(path, keyA, "p")
	if err != nil || dn != 1 {
		t.Fatalf("decrypt p with the run key: n=%d err=%v, want 1/nil", dn, err)
	}
	data, _ = os.ReadFile(path)
	if !strings.Contains(string(data), encT) {
		t.Fatal("decrypting p must not touch the stranded profile")
	}

	// File-wide and stranded-profile runs still refuse the different salt.
	for _, profile := range []string{"", "q"} {
		path, _ = writeMixedSaltConfig(t, keyT, saltT)
		n, err = EncryptFile(path, keyA, saltA, profile, false)
		if err == nil || !strings.Contains(err.Error(), "different key") {
			t.Fatalf("encrypt profile %q over a different salt: err=%v, want refusal", profile, err)
		}
		if n != 0 {
			t.Fatalf("profile %q: n = %d, want 0 (nothing written on refusal)", profile, n)
		}
		data, _ = os.ReadFile(path)
		if strings.Count(string(data), "enc:v3:") != 1 {
			t.Fatalf("profile %q: refused run must leave the file untouched", profile)
		}
	}
}

func TestEncryptFileRejectsUnreadableOutsideWrittenProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := "profiles:\n  q:\n    env:\n      OLD: enc:v2:YWJj\n  p:\n    env:\n      TOKEN: plain\n"
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	salt := make([]byte, 16)
	n, err := EncryptFile(path, key, salt, "p", false)
	if err == nil || !strings.Contains(err.Error(), "unsupported encrypted value") {
		t.Fatalf("err = %v, want unsupported encrypted value (unreadable values fail file-wide)", err)
	}
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
}

// TestEncryptFileJoinsFileWideSameSaltEra pins the era rule: when the written
// profile holds no encrypted values but the file carries same-salt values in
// another profile, new values join their KDF-parameter era. Same salt with
// different params derives a different key, so stamping CurrentParams beside
// an older era would leave a file no single passphrase recovers.
func TestEncryptFileJoinsFileWideSameSaltEra(t *testing.T) {
	key := make([]byte, 32)
	salt := []byte("aaaaaaaaaaaaaaaa")
	old := crypto.Argon2Params{Time: 2, Memory: 16 * 1024, Threads: 1}
	enc, err := crypto.EncryptValueWithParams("old", key, salt, old)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := "profiles:\n  q:\n    env:\n      OLD: " + enc + "\n  p:\n    env:\n      TOKEN: plain\n"
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := EncryptFile(path, key, salt, "p", false); err != nil || n != 1 {
		t.Fatalf("encrypt p: n=%d err=%v, want 1/nil", n, err)
	}
	prof, _, _, err := ReadProfile(path, "p")
	if err != nil {
		t.Fatal(err)
	}
	gotSalt, gotParams, err := crypto.SaltFromValue(prof.Env["TOKEN"])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotSalt, salt) || gotParams != old {
		t.Fatalf("TOKEN era = %v (salt match %t), want the file's era %v", gotParams, bytes.Equal(gotSalt, salt), old)
	}
	if _, _, _, err := FirstSaltAndSample(path); err != nil {
		t.Fatalf("file must stay one era for passphrase recovery: %v", err)
	}
}

// TestDecryptFileWrongKeyActionableError pins the pre-verify contract: a key
// that cannot read the target profile's values fails once, naming the
// recovery path, instead of a per-value wrong-key error mid-run. The advised
// remedy must be executable even when a stale key cache blocks plain decrypt —
// keygen --force with the original passphrase installs the matching key.
func TestDecryptFileWrongKeyActionableError(t *testing.T) {
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	keyB[0] = 1
	salt := []byte("aaaaaaaaaaaaaaaa")
	enc, err := crypto.EncryptValueWithParams("v", keyA, salt, crypto.CurrentParams)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      TOKEN: "+enc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = DecryptFile(path, keyB, "")
	if err == nil || !strings.Contains(err.Error(), "keygen --force") {
		t.Fatalf("err = %v, want wrong-key error naming the keygen --force remedy", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), enc) {
		t.Fatal("a refused decrypt must not rewrite the file")
	}
}

func TestCryptPathsRejectNonMappingProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("profiles: [a]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	salt := make([]byte, 16)
	if _, err := EncryptFile(path, key, salt, "", true); err == nil || !strings.Contains(err.Error(), "profiles is not a mapping") {
		t.Fatalf("encrypt: err=%v, want profiles-not-mapping", err)
	}
	if _, err := DecryptFile(path, key, ""); err == nil || !strings.Contains(err.Error(), "profiles is not a mapping") {
		t.Fatalf("decrypt: err=%v, want profiles-not-mapping", err)
	}
	if _, _, _, err := FirstSaltAndSample(path); err == nil || !strings.Contains(err.Error(), "profiles is not a mapping") {
		t.Fatalf("salt scan: err=%v, want profiles-not-mapping", err)
	}
}

func TestCryptPathsErrorOnMissingProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := "profiles:\n  a:\n    env:\n      K: v\n"
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	salt := make([]byte, 16)
	n, err := EncryptFile(path, key, salt, "nosuch", false)
	if err == nil || !strings.Contains(err.Error(), `profile "nosuch" not found`) {
		t.Fatalf("encrypt: n=%d err=%v, want not found", n, err)
	}
	n, err = DecryptFile(path, key, "nosuch")
	if err == nil || !strings.Contains(err.Error(), `profile "nosuch" not found`) {
		t.Fatalf("decrypt: n=%d err=%v, want not found", n, err)
	}
}
