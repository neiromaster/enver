package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/spf13/cobra"
)

func TestCompleteProfileForCryptAndDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	want := []string{"dev", "prod", "stage"}
	for _, p := range want {
		if err := config.UpsertProfile(path, p, config.Profile{Env: map[string]string{"A": "1"}}, false, false); err != nil {
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
	if err := config.UpsertProfile(local, "p", config.Profile{Env: map[string]string{"API_KEY": "secret"}}, false, false); err != nil {
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
	if scan.params != crypto.CurrentParams {
		t.Fatalf("params = %+v, want %+v", scan.params, crypto.CurrentParams)
	}

	// A corrupt config must surface as an error, not be skipped.
	if err := os.WriteFile(local, []byte("[1, 2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scanConfigCrypt(); err == nil {
		t.Fatal("corrupt config must be an error")
	}
}

func TestScanConfigCryptConflictingEras(t *testing.T) {
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	globalFlags.configPath = filepath.Join(dir, "global.yaml")

	key := make([]byte, 32)
	encA, err := crypto.EncryptValueWithParams("a", key, []byte("0123456789abcdef"), crypto.CurrentParams)
	if err != nil {
		t.Fatal(err)
	}
	encB, err := crypto.EncryptValueWithParams("b", key, []byte("fedcba9876543210"), crypto.CurrentParams)
	if err != nil {
		t.Fatal(err)
	}
	cfg := "profiles:\n  p:\n    env:\n      A: \"" + encA + "\"\n      B: \"" + encB + "\"\n"
	if err := os.WriteFile(config.LocalPath(), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scanConfigCrypt(); err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("scan must reject values from two eras, got: %v", err)
	}
}

func TestKeygenRandomIgnoresBrokenConfigs(t *testing.T) {
	// --random is the non-interactive bootstrap path: with no key to overwrite
	// there is nothing to strand, so unreadable configs must not block it.
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	globalFlags.configPath = filepath.Join(dir, "global.yaml")
	globalFlags.keyPath = filepath.Join(dir, "key")
	prev := keygenRandom
	keygenRandom = true
	t.Cleanup(func() { keygenRandom = prev })

	global := config.GlobalPath(globalFlags.configPath)
	if err := os.WriteFile(global, []byte("profiles:\n  p:\n    env:\n      TOKEN: enc:v2:YWJj\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.LocalPath(), []byte("[1, 2"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := keygenCmd.RunE(keygenCmd, nil); err != nil {
		t.Fatalf("keygen --random must not read configs when nothing can be stranded: %v", err)
	}
	if _, err := os.Stat(globalFlags.keyPath); err != nil {
		t.Fatalf("key not written: %v", err)
	}
}

func TestKeygenForceRejectsForeignEnc(t *testing.T) {
	// A forced overwrite cannot judge what it would strand while configs hold
	// values this build cannot read, so the risk-gating scan rejects them.
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	globalFlags.configPath = filepath.Join(dir, "global.yaml")
	globalFlags.keyPath = filepath.Join(dir, "key")
	prev := keygenRandom
	keygenRandom = true
	t.Cleanup(func() { keygenRandom = prev })

	if err := crypto.GenerateKey(globalFlags.keyPath, true); err != nil {
		t.Fatal(err)
	}
	if err := keygenCmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keygenCmd.Flags().Set("force", "false") })

	global := config.GlobalPath(globalFlags.configPath)
	cfgContent := "profiles:\n  p:\n    env:\n      TOKEN: enc:v2:YWJj\n"
	if err := os.WriteFile(global, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := keygenCmd.RunE(keygenCmd, nil); err == nil || !strings.Contains(err.Error(), "unsupported encrypted value") {
		t.Fatalf("forced keygen must reject config with foreign enc: values, got: %v", err)
	}
}

func TestKeygenReusesParamsFromConfig(t *testing.T) {
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	globalFlags.configPath = filepath.Join(dir, "global.yaml")

	salt := make([]byte, crypto.SaltSize)
	custom := crypto.Argon2Params{Time: 2, Memory: 16 * 1024, Threads: 1}
	key, err := crypto.DeriveKey("test-pass", salt, custom)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.EncryptValueWithParams("secret", key, salt, custom)
	if err != nil {
		t.Fatal(err)
	}

	global := config.GlobalPath(globalFlags.configPath)
	cfgContent := "profiles:\n  p:\n    env:\n      TOKEN: " + enc + "\n"
	if err := os.WriteFile(global, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	scan, err := scanConfigCrypt()
	if err != nil {
		t.Fatalf("scanConfigCrypt: %v", err)
	}
	if !scan.hasEncrypted {
		t.Fatal("scan should detect encrypted values")
	}
	if scan.salt == nil {
		t.Fatal("scan should capture salt")
	}
	if scan.params != custom {
		t.Fatalf("params = %+v, want %+v", scan.params, custom)
	}

	globalFlags.keyPath = filepath.Join(dir, "key")
	oldPassword := app.PromptPassphrase
	oldInteractive := app.Interactive
	app.PromptPassphrase = func(prompt string) (string, error) { return "test-pass", nil }
	app.Interactive = func() bool { return true }
	t.Cleanup(func() {
		app.PromptPassphrase = oldPassword
		app.Interactive = oldInteractive
	})

	if err := keygenCmd.RunE(keygenCmd, nil); err != nil {
		t.Fatalf("keygen passphrase failed: %v", err)
	}

	derivedKey, err := crypto.DeriveKey("test-pass", salt, custom)
	if err != nil {
		t.Fatalf("derive key with custom params: %v", err)
	}
	n, err := config.DecryptFile(global, derivedKey, "")
	if err != nil {
		t.Fatalf("decrypt with derived key: %v", err)
	}
	if n != 1 {
		t.Fatalf("decrypted %d values, want 1", n)
	}
}

func TestKeygenVerifiesPassphraseAgainstConfig(t *testing.T) {
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	globalFlags.configPath = filepath.Join(dir, "global.yaml")
	globalFlags.keyPath = filepath.Join(dir, "key")

	// Cheap params keep the derivation fast; the format is what matters.
	params := crypto.Argon2Params{Time: 2, Memory: 16 * 1024, Threads: 1}
	salt := []byte("0123456789abcdef")
	right, err := crypto.DeriveKey("right-pass", salt, params)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.EncryptValueWithParams("secret", right, salt, params)
	if err != nil {
		t.Fatal(err)
	}
	global := config.GlobalPath(globalFlags.configPath)
	if err := os.WriteFile(global, []byte("profiles:\n  p:\n    env:\n      TOKEN: "+enc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldPassword := app.PromptPassphrase
	oldInteractive := app.Interactive
	app.Interactive = func() bool { return true }
	t.Cleanup(func() {
		app.PromptPassphrase = oldPassword
		app.Interactive = oldInteractive
	})

	// A mistyped passphrase derives the wrong key for the reused salt: keygen
	// must refuse instead of caching a key the values never open with.
	app.PromptPassphrase = func(prompt string) (string, error) { return "wrong-pass", nil }
	if err := keygenCmd.RunE(keygenCmd, nil); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("keygen with wrong passphrase: err=%v, want a mismatch error", err)
	}
	if _, err := os.Stat(globalFlags.keyPath); !os.IsNotExist(err) {
		t.Fatal("mismatched passphrase must not write a key file")
	}

	app.PromptPassphrase = func(prompt string) (string, error) { return "right-pass", nil }
	if err := keygenCmd.RunE(keygenCmd, nil); err != nil {
		t.Fatalf("keygen with right passphrase: %v", err)
	}
	key, _, err := crypto.LoadKey(globalFlags.keyPath)
	if err != nil {
		t.Fatalf("load written key: %v", err)
	}
	plain, err := crypto.DecryptValue(enc, key)
	if err != nil || plain != "secret" {
		t.Fatalf("decrypt with written key = (%q, %v), want (secret, nil)", plain, err)
	}
}

// TestKeygenDeclinedConfirmAbortsCleanly pins the abort contract: declining the
// stranding confirm is a choice, not a failure — keygen exits 0 with the shared
// abort notice and leaves the old key in place.
func TestKeygenDeclinedConfirmAbortsCleanly(t *testing.T) {
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	globalFlags.configPath = filepath.Join(dir, "global.yaml")
	globalFlags.keyPath = filepath.Join(dir, "key")

	params := crypto.Argon2Params{Time: 2, Memory: 16 * 1024, Threads: 1}
	salt := []byte("0123456789abcdef")
	right, err := crypto.DeriveKey("right-pass", salt, params)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.EncryptValueWithParams("secret", right, salt, params)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.GlobalPath(globalFlags.configPath), []byte("profiles:\n  p:\n    env:\n      TOKEN: "+enc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := crypto.GenerateKey(globalFlags.keyPath, true); err != nil {
		t.Fatal(err)
	}
	staleKey, _, err := crypto.LoadKey(globalFlags.keyPath)
	if err != nil {
		t.Fatal(err)
	}

	oldPassword, oldInteractive, oldConfirm := app.PromptPassphrase, app.Interactive, uiConfirm
	app.Interactive = func() bool { return true }
	app.PromptPassphrase = func(prompt string) (string, error) { return "wrong-pass", nil }
	uiConfirm = func(string, bool) (bool, error) { return false, nil }
	t.Cleanup(func() {
		app.PromptPassphrase, app.Interactive, uiConfirm = oldPassword, oldInteractive, oldConfirm
	})
	if err := keygenCmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keygenCmd.Flags().Set("force", "false") })

	var out bytes.Buffer
	keygenCmd.SetOut(&out)
	t.Cleanup(func() { keygenCmd.SetOut(nil) })

	if err := keygenCmd.RunE(keygenCmd, nil); err != nil {
		t.Fatalf("declined confirm must exit 0, got: %v", err)
	}
	if !strings.Contains(out.String(), "aborted") {
		t.Fatalf("output = %q, want the abort notice", out.String())
	}
	keptKey, _, err := crypto.LoadKey(globalFlags.keyPath)
	if err != nil || !bytes.Equal(staleKey, keptKey) {
		t.Fatal("declined confirm must leave the old key in place")
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

func TestKeygenForceMatchingPassphraseSkipsStrandingConfirm(t *testing.T) {
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	globalFlags.configPath = filepath.Join(dir, "global.yaml")
	globalFlags.keyPath = filepath.Join(dir, "key")

	params := crypto.Argon2Params{Time: 2, Memory: 16 * 1024, Threads: 1}
	salt := []byte("0123456789abcdef")
	right, err := crypto.DeriveKey("right-pass", salt, params)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.EncryptValueWithParams("secret", right, salt, params)
	if err != nil {
		t.Fatal(err)
	}
	global := config.GlobalPath(globalFlags.configPath)
	if err := os.WriteFile(global, []byte("profiles:\n  p:\n    env:\n      TOKEN: "+enc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A stale random key cache: force-overwriting it with the matching
	// passphrase key is recovery, not stranding.
	if err := crypto.GenerateKey(globalFlags.keyPath, true); err != nil {
		t.Fatal(err)
	}

	oldPassword, oldInteractive, oldConfirm := app.PromptPassphrase, app.Interactive, uiConfirm
	app.Interactive = func() bool { return true }
	app.PromptPassphrase = func(prompt string) (string, error) { return "right-pass", nil }
	uiConfirm = func(msg string, _ bool) (bool, error) {
		t.Errorf("matching passphrase must not be confirmed as stranding: %q", msg)
		return false, nil
	}
	t.Cleanup(func() {
		app.PromptPassphrase, app.Interactive, uiConfirm = oldPassword, oldInteractive, oldConfirm
	})
	if err := keygenCmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keygenCmd.Flags().Set("force", "false") })

	if err := keygenCmd.RunE(keygenCmd, nil); err != nil {
		t.Fatalf("keygen --force with matching passphrase: %v", err)
	}
	key, _, err := crypto.LoadKey(globalFlags.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if plain, derr := crypto.DecryptValue(enc, key); derr != nil || plain != "secret" {
		t.Fatalf("decrypt with installed key = (%q, %v), want (secret, nil)", plain, derr)
	}
}

func TestKeygenForceWrongPassphraseConfirmsStranding(t *testing.T) {
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	globalFlags.configPath = filepath.Join(dir, "global.yaml")
	globalFlags.keyPath = filepath.Join(dir, "key")

	params := crypto.Argon2Params{Time: 2, Memory: 16 * 1024, Threads: 1}
	salt := []byte("0123456789abcdef")
	right, err := crypto.DeriveKey("right-pass", salt, params)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.EncryptValueWithParams("secret", right, salt, params)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.GlobalPath(globalFlags.configPath), []byte("profiles:\n  p:\n    env:\n      TOKEN: "+enc+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := crypto.GenerateKey(globalFlags.keyPath, true); err != nil {
		t.Fatal(err)
	}

	confirmed := false
	oldPassword, oldInteractive, oldConfirm := app.PromptPassphrase, app.Interactive, uiConfirm
	app.Interactive = func() bool { return true }
	app.PromptPassphrase = func(prompt string) (string, error) { return "wrong-pass", nil }
	uiConfirm = func(msg string, _ bool) (bool, error) {
		confirmed = true
		if !strings.Contains(msg, "strands") {
			t.Errorf("confirm prompt should say what overwriting does: %q", msg)
		}
		return true, nil
	}
	t.Cleanup(func() {
		app.PromptPassphrase, app.Interactive, uiConfirm = oldPassword, oldInteractive, oldConfirm
	})
	if err := keygenCmd.Flags().Set("force", "true"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keygenCmd.Flags().Set("force", "false") })

	if err := keygenCmd.RunE(keygenCmd, nil); err != nil {
		t.Fatalf("accepted confirm must install the key: %v", err)
	}
	if !confirmed {
		t.Fatal("a wrong passphrase under --force really strands the values; it must go through the confirm gate")
	}
}
