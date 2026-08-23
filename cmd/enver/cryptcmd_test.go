package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/spf13/cobra"
)

func TestCompleteProfileForCryptAndDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	want := []string{"dev", "prod", "stage"}
	for _, p := range want {
		if err := config.UpsertProfile(path, p, config.Profile{Env: map[string]string{"A": "1"}}, false, false, nil); err != nil {
			t.Fatal(err)
		}
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")
	cmd.Flags().Bool("global", false, "")
	_ = cmd.Flags().Set("config", path)
	_ = cmd.Flags().Set("global", "true")

	for _, c := range []*cobra.Command{encryptCmd, decryptCmd, defaultCmd} {
		got, dir := c.ValidArgsFunction(cmd, nil, "")
		if dir != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("%s: directive=%v, want NoFileComp", c.Use, dir)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", c.Use, got, want)
		}
	}
}

func TestKeygenRisk(t *testing.T) {
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	globalFlags.configPath = filepath.Join(dir, "global.yaml")

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
	dir := chdirTemp(t)
	saveGlobalFlags(t)
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

func TestScanConfigCrypt(t *testing.T) {
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	globalFlags.configPath = filepath.Join(dir, "global.yaml")

	// Missing configs: no salt, nothing encrypted.
	scan, err := scanConfigCrypt()
	if err != nil {
		t.Fatalf("scanConfigCrypt: %v", err)
	}
	if scan.hasEncrypted || scan.salt != nil {
		t.Fatalf("empty configs: hasEncrypted=%v salt=%v", scan.hasEncrypted, scan.salt)
	}

	// Encrypt a value in the local layer: salt and flag are detected.
	key := make([]byte, 32)
	local := config.LocalPath()
	if err := config.UpsertProfile(local, "p", config.Profile{Env: map[string]string{"API_KEY": "secret"}}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := config.EncryptFile(local, key, []byte("0123456789abcdef"), "", false); err != nil {
		t.Fatal(err)
	}
	scan, err = scanConfigCrypt()
	if err != nil {
		t.Fatalf("scanConfigCrypt: %v", err)
	}
	if !scan.hasEncrypted {
		t.Fatal("encrypted value must be detected")
	}
	if string(scan.salt) != "0123456789abcdef" {
		t.Fatalf("salt = %q, want 0123456789abcdef", scan.salt)
	}

	// A corrupt config must surface as an error, not be skipped.
	if err := os.WriteFile(local, []byte("[1, 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scanConfigCrypt(); err == nil {
		t.Fatal("corrupt config must be an error")
	}
}

func TestKeygenPath(t *testing.T) {
	saveGlobalFlags(t)
	globalFlags.keyPath = ""
	if got, want := keygenPath(), crypto.KeyFilePath(); got != want {
		t.Fatalf("default keygen path = %q, want %q", got, want)
	}
	globalFlags.keyPath = "/custom/key"
	if got := keygenPath(); got != "/custom/key" {
		t.Fatalf("keygen path with --key = %q, want /custom/key", got)
	}
}
