package config

import (
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
	encT, err := crypto.EncryptValue("old", keyT, saltT)
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
