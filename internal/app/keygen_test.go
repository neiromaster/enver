package app

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
)

func TestKeygenRisk(t *testing.T) {
	dir := t.TempDir()

	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	keyB[0] = 1

	keyPath := filepath.Join(dir, "key")

	noEnc := func() (bool, error) { return false, nil }
	enc := func() (bool, error) { return true, nil }

	risk, err := keygenRisk(true, keyPath, keyA, noEnc)
	if err != nil {
		t.Fatalf("keygenRisk: %v", err)
	}
	if risk {
		t.Fatal("no existing key file must not be a risk")
	}
	if err := crypto.WriteKeyCache(keyPath, crypto.NewKeyCache(make([]byte, 16), keyA)); err != nil {
		t.Fatal(err)
	}
	if risk, err = keygenRisk(false, keyPath, keyA, enc); err != nil {
		t.Fatal(err)
	} else if risk {
		t.Fatal("without --force there is no overwrite")
	}
	if risk, err = keygenRisk(true, keyPath, keyA, enc); err != nil {
		t.Fatal(err)
	} else if risk {
		t.Fatal("rewriting the same key must be safe")
	}
	if risk, err = keygenRisk(true, keyPath, keyB, noEnc); err != nil {
		t.Fatal(err)
	} else if risk {
		t.Fatal("different key with no encrypted values must not warn")
	}
	if risk, err = keygenRisk(true, keyPath, nil, noEnc); err != nil {
		t.Fatal(err)
	} else if risk {
		t.Fatal("random key with no encrypted values must not warn")
	}
	if risk, err = keygenRisk(true, keyPath, keyB, enc); err != nil {
		t.Fatal(err)
	} else if !risk {
		t.Fatal("different key with encrypted values must warn")
	}
	if risk, err = keygenRisk(true, keyPath, keyA, enc); err != nil {
		t.Fatal(err)
	} else if risk {
		t.Fatal("same key must stay safe even with encrypted values")
	}
	if risk, err = keygenRisk(true, keyPath, nil, enc); err != nil {
		t.Fatal(err)
	} else if !risk {
		t.Fatal("random key with encrypted values must warn")
	}
}

func TestKeygenRiskCorruptKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key")
	if err := os.WriteFile(keyPath, []byte("not a key cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	// An existing-but-unreadable key may still protect encrypted values; forcing
	// a new key must refuse rather than overwrite silently.
	if _, err := keygenRisk(true, keyPath, nil, func() (bool, error) { return true, nil }); err == nil {
		t.Fatal("corrupt key file must be an error")
	}
}

// TestKeygenDeclinedConfirmReturnsErrAborted pins the sentinel contract: a
// declined stranding confirm is reported as ErrAborted and the key file is
// left untouched.
func TestKeygenDeclinedConfirmReturnsErrAborted(t *testing.T) {
	dir := t.TempDir()
	// Cheap params keep the derivation fast; the format is what matters.
	params := crypto.Argon2Params{Time: 2, Memory: 16 * 1024, Threads: 1}
	salt := []byte("0123456789abcdef")
	// The config values belong to a raw key no passphrase derives, so the
	// prompted passphrase cannot decrypt the sample and the stranding
	// confirm fires.
	cfgKey := make([]byte, 32)
	enc, err := crypto.EncryptValueWithParams("secret", cfgKey, salt, params)
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(fixture, []byte("profiles:\n  p:\n    env:\n      TOKEN: "+enc+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "key")
	// A valid key cache must already sit at path: a corrupt one is refused in
	// the risk gate before the confirm is ever reached.
	if err := crypto.GenerateKey(keyPath, true); err != nil {
		t.Fatal(err)
	}
	staleKey, _, err := crypto.LoadKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}

	oldPrompt, oldInteractive := PromptPassphrase, Interactive
	PromptPassphrase = func(string) (string, error) { return "right-pass", nil }
	Interactive = func() bool { return true }
	t.Cleanup(func() { PromptPassphrase, Interactive = oldPrompt, oldInteractive })

	var scan crypto.SaltScan
	if err := config.ScanCrypt(fixture, &scan); err != nil {
		t.Fatalf("ScanCrypt: %v", err)
	}
	salt, params, sample := scan.Result()

	err = Keygen(KeygenOptions{
		Path:  keyPath,
		Force: true,
		Scan: func() (CryptScan, error) {
			return CryptScan{Salt: salt, Params: params, Sample: sample}, nil
		},
		Confirm: func(string, bool) (bool, error) { return false, nil },
	})
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("Keygen decline = %v, want ErrAborted", err)
	}
	keptKey, _, err := crypto.LoadKey(keyPath)
	if err != nil || !bytes.Equal(staleKey, keptKey) {
		t.Fatal("a declined keygen must not touch the key file")
	}
}
