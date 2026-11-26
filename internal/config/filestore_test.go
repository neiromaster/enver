package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreDir(t *testing.T) {
	for _, c := range []struct{ file, want string }{
		{filepath.Join("repo", "team.yaml"), filepath.Join("repo", ".enver", "team.yaml")},
		{filepath.Join("repo", "a.yml"), filepath.Join("repo", ".enver", "a.yml")},
	} {
		if got := FileStoreDir(c.file); got != c.want {
			t.Errorf("FileStoreDir(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}

func TestLoadStandalone(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "team.yaml")
	if err := os.WriteFile(file, []byte("profiles:\n  base:\n    env:\n      K: v\nremotes:\n  r:\n    url: https://example.invalid/r.yaml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := FileStoreDir(file)
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "r.yaml"), []byte("profiles:\n  default:\n    env:\n      R: from-store\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "enver"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "enver", "config.yaml"), []byte("profiles:\n  ghost:\n    env:\n      G: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := RemotesDir
	RemotesDir = func() string { return store }
	t.Cleanup(func() { RemotesDir = old })
	cfg, err := LoadStandalone(file)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profiles["base"].Env["K"] != "v" || cfg.Profiles["r/default"].Env["R"] != "from-store" {
		t.Fatalf("profiles = %+v", cfg.Profiles)
	}
	if _, ok := cfg.Profiles["ghost"]; ok {
		t.Fatal("the global config leaked into a standalone load")
	}
	if cfg.ProfileLayer["base"] != LayerFile || cfg.ProfileLayer["r/default"] != "remote:r" {
		t.Fatalf("layers = %v", cfg.ProfileLayer)
	}
	empty, err := LoadStandalone(filepath.Join(dir, "missing.yaml"))
	if err != nil || len(empty.Profiles) != 0 {
		t.Fatalf("a missing file must load empty, got %+v, %v", empty.Profiles, err)
	}
}

func TestHintCommand(t *testing.T) {
	t.Cleanup(func() { HintFlags = "" })
	if got := HintCommand("default", "<profile>"); got != "enver default <profile>" {
		t.Fatalf("HintCommand with one arg = %q", got)
	}
	if got := HintCommand("decrypt"); got != "enver decrypt" {
		t.Fatalf("HintCommand with no args = %q", got)
	}
	if got := HintCommand("x", "p", "--", "cmd sub"); got != "enver x p -- cmd sub" {
		t.Fatalf("HintCommand with several args = %q", got)
	}
	HintFlags = "--file team.yaml"
	if got := HintCommand("default", "<profile>"); got != "enver default --file team.yaml <profile>" {
		t.Fatalf("HintCommand with flags = %q", got)
	}
	if got := HintCommand("encrypt", "p", "--all"); got != "enver encrypt --file team.yaml p --all" {
		t.Fatalf("HintCommand with flags and several args = %q", got)
	}
}

func TestPullCommand(t *testing.T) {
	t.Cleanup(func() { HintFlags = "" })
	if got := PullCommand("claude"); got != "enver pull claude" {
		t.Fatalf("PullCommand = %q", got)
	}
	HintFlags = "--file team.yaml"
	if got := PullCommand("--rm", "base"); got != "enver pull --file team.yaml --rm base" {
		t.Fatalf("PullCommand with flags = %q", got)
	}
}
