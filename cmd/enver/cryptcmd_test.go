package main

import (
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

	if keygenRisk(true, keyPath, keyA) {
		t.Fatal("no existing key file must not be a risk")
	}
	if err := crypto.WriteKeyCache(keyPath, crypto.NewKeyCache(make([]byte, 16), keyA)); err != nil {
		t.Fatal(err)
	}
	if keygenRisk(false, keyPath, keyA) {
		t.Fatal("without --force there is no overwrite")
	}
	if keygenRisk(true, keyPath, keyA) {
		t.Fatal("rewriting the same key must be safe")
	}
	if keygenRisk(true, keyPath, keyB) {
		t.Fatal("different key with no encrypted values must not warn")
	}
	if keygenRisk(true, keyPath, nil) {
		t.Fatal("random key with no encrypted values must not warn")
	}

	local := config.LocalPath()
	if err := config.UpsertProfile(local, "p", config.Profile{Env: map[string]string{"API_KEY": "secret"}}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := config.EncryptFile(local, keyA, "", false, []byte("0123456789abcdef")); err != nil {
		t.Fatal(err)
	}
	if !keygenRisk(true, keyPath, keyB) {
		t.Fatal("different key with encrypted values must warn")
	}
	if keygenRisk(true, keyPath, keyA) {
		t.Fatal("same key must stay safe even with encrypted values")
	}
	if !keygenRisk(true, keyPath, nil) {
		t.Fatal("random key with encrypted values must warn")
	}
}
