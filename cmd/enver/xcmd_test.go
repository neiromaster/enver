package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/crypto"
)

// TestXNonInteractiveNoKeyFailsLoudly pins the runner recovery behavior: with
// an encrypted (enc:v3) config and no key source, `enver x` must fail loudly
// instead of prompting or hanging. The recovery inside app.ResolveKeyOrPrompt
// gates on app.Interactive (bound at init), so pin the func vars directly.
func TestXNonInteractiveNoKeyFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("ENVER_KEY", "")

	salt := []byte("0123456789abcdef")
	key, err := crypto.DeriveKey("hunter2", salt, crypto.CurrentParams)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	enc, err := crypto.EncryptValueWithParams("secret", key, salt, crypto.CurrentParams)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	cfgPath := filepath.Join(dir, "enver", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := fmt.Sprintf("profiles:\n  anth:\n    env:\n      API_KEY: %s\n", enc)
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	withGlobalConfig(t, cfgPath)

	prevInteractive := app.Interactive
	app.Interactive = func() bool { return false }
	t.Cleanup(func() { app.Interactive = prevInteractive })
	// Safety net: if interactive were ever (incorrectly) true, fail before the
	// runner can exec a child in the test process.
	prevPrompt := app.PromptPassphrase
	app.PromptPassphrase = func(string) (string, error) { return "", errors.New("should not prompt in non-interactive mode") }
	t.Cleanup(func() { app.PromptPassphrase = prevPrompt })

	rootCmd.SetArgs([]string{"x", "anth", "--", "sh", "-c", "true"})
	if err := rootCmd.Execute(); err == nil || !strings.Contains(err.Error(), "no key found") {
		t.Fatalf("expected no-key-found error, got: %v", err)
	}
}
