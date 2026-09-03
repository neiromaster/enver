package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/neiromaster/enver/internal/runner"
)

func TestParseProfileAndCmd(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		dashAt   int
		wantProf string
		wantCmd  []string
	}{
		{"profile then command", []string{"anth", "claude"}, 1, "anth", []string{"claude"}},
		{"default profile via dash", []string{"claude"}, 0, "", []string{"claude"}},
		{"command keeps its own flags", []string{"anth", "claude", "--model", "x"}, 1, "anth", []string{"claude", "--model", "x"}},
		{"no dash: profile only", []string{"anth"}, -1, "anth", nil},
		{"no dash: empty", nil, -1, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prof, cmd := ParseProfileAndCmd(c.args, c.dashAt)
			if prof != c.wantProf {
				t.Errorf("profile = %q, want %q", prof, c.wantProf)
			}
			if !sliceEq(cmd, c.wantCmd) {
				t.Errorf("cmdArgs = %v, want %v", cmd, c.wantCmd)
			}
		})
	}
}

func TestRunRequiresCommand(t *testing.T) {
	// No command after `--` → must error before touching config or exec.
	cases := []struct {
		name   string
		args   []string
		dashAt int
	}{
		{"profile but no command", []string{"anth"}, -1},
		{"nothing at all", nil, -1},
		{"dash but nothing after", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Run(c.args, c.dashAt, Options{Name: "enver x"})
			if err == nil {
				t.Fatal("Run returned nil, want an error")
			}
			if !strings.Contains(err.Error(), "requires a command") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestResolveLazyKey(t *testing.T) {
	// A plaintext profile resolves with no key available.
	cfg := config.Config{Profiles: map[string]config.Profile{
		"p": {Env: map[string]string{"MODEL": "claude-sonnet-5"}},
	}}
	t.Setenv("ENVER_KEY", "")
	r, err := resolve(cfg, "p", Options{})
	if err != nil {
		t.Fatalf("plaintext resolve: %v", err)
	}
	if r.Env["MODEL"] != "claude-sonnet-5" {
		t.Fatalf("env = %v", r.Env)
	}
	if len(r.Chain) != 1 || r.Chain[0] != "p" {
		t.Fatalf("chain = %v", r.Chain)
	}

	// An encrypted profile with no key available → error. Encrypt with a real
	// key file so the later decrypt step can reuse the same file.
	dir := t.TempDir()
	kpath := filepath.Join(dir, "key")
	if err := crypto.GenerateKey(kpath, true); err != nil {
		t.Fatal(err)
	}
	key, salt, err := crypto.LoadKey(kpath)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.EncryptValueWithParams("secret-value", key, salt, crypto.CurrentParams)
	if err != nil {
		t.Fatal(err)
	}
	encCfg := config.Config{Profiles: map[string]config.Profile{
		"e": {Env: map[string]string{"API_KEY": enc}},
	}}
	if _, err := resolve(encCfg, "e", Options{KeyPath: "/nonexistent/key"}); err == nil {
		t.Fatal("encrypted profile without key should error")
	}

	// Same profile with the right key file → decrypts.
	r2, err := resolve(encCfg, "e", Options{KeyPath: kpath})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if r2.Env["API_KEY"] != "secret-value" {
		t.Fatalf("decrypted = %q, want secret-value", r2.Env["API_KEY"])
	}
}

func TestResolveExpandsAndNoExpand(t *testing.T) {
	cfg := config.Config{Profiles: map[string]config.Profile{
		"p": {Env: map[string]string{"HOST": "h", "URL": "$HOST/x", "SEC": "$S"}},
	}}
	t.Setenv("S", "sec")

	// default: expanded.
	got, err := resolve(cfg, "p", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Env["URL"] != "h/x" || got.Env["SEC"] != "sec" {
		t.Errorf("Resolve did not expand: %+v", got.Env)
	}
	// NoExpand: raw templates preserved.
	got, err = resolve(cfg, "p", Options{NoExpand: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Env["URL"] != "$HOST/x" || got.Env["SEC"] != "$S" {
		t.Errorf("NoExpand should keep raw: %+v", got.Env)
	}
}

func TestResolveExpansionSeesShellThroughUnset(t *testing.T) {
	// An unset key is simply absent from the resolved env, so the shell value
	// passes through: expansion sees the same shell env the child will get.
	t.Setenv("ENVER_TEST_TOKEN_SRC", "live-value")

	fenced := config.Config{Profiles: map[string]config.Profile{
		"p": {Env: map[string]string{"DERIVED": "${ENVER_TEST_TOKEN_SRC}"}, Unset: config.Unsets{"ENVER_TEST_TOKEN_SRC"}},
	}}
	got, err := resolve(fenced, "p", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Env["DERIVED"] != "live-value" {
		t.Fatalf("DERIVED = %q, want live-value: the shell value passes through an unset", got.Env["DERIVED"])
	}
	if _, ok := got.Env["ENVER_TEST_TOKEN_SRC"]; ok {
		t.Fatalf("unset key leaked into the resolved env: %v", got.Env)
	}
}

func TestRunNoProfileNoDefault(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := "profiles:\n  dev:\n    env:\n      FOO: bar\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{ConfigPath: cfgPath, NoLocal: true, Name: "enver x"}
	err := Run([]string{"--", "echo", "hi"}, 0, opts)
	if err == nil {
		t.Fatal("Run returned nil, want an error")
	}
	if !strings.Contains(err.Error(), "no profile specified and no default set") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProfileOrDefault(t *testing.T) {
	if p, err := profileOrDefault("", ""); err == nil {
		t.Fatal("empty profile and empty default should error")
	} else if p != "" {
		t.Fatalf("got profile %q on error, want empty", p)
	}
	if p, err := profileOrDefault("", "dev"); err != nil || p != "dev" {
		t.Fatalf("empty profile should fall back to default: p=%q err=%v", p, err)
	}
	if p, err := profileOrDefault("prod", "dev"); err != nil || p != "prod" {
		t.Fatalf("explicit profile should win over default: p=%q err=%v", p, err)
	}
}

func TestResolveKeyFromCache(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "key")
	salt := []byte("0123456789abcdef")
	key := make([]byte, 32)
	if err := crypto.WriteKeyCache(cachePath, crypto.NewKeyCache(salt, key)); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	got, gotSalt, err := resolveKey(Options{KeyPath: cachePath})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !bytes.Equal(got, key) || !bytes.Equal(gotSalt, salt) {
		t.Fatalf("key=%x salt=%x, want %x/%x", got, gotSalt, key, salt)
	}
}

func TestResolveKeyOrPromptSkipsSourceWhenKeyResolves(t *testing.T) {
	dir := t.TempDir()
	cachePath := filepath.Join(dir, "key")
	salt := []byte("0123456789abcdef")
	key := make([]byte, 32)
	if err := crypto.WriteKeyCache(cachePath, crypto.NewKeyCache(salt, key)); err != nil {
		t.Fatalf("write cache: %v", err)
	}
	called := false
	got, gotSalt, err := ResolveKeyOrPrompt(Options{KeyPath: cachePath}, func() ([]byte, crypto.Argon2Params, string, error) {
		called = true
		return nil, crypto.Argon2Params{}, "", nil
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if called {
		t.Fatal("salt source must stay idle when a key resolves")
	}
	if !bytes.Equal(got, key) || !bytes.Equal(gotSalt, salt) {
		t.Fatalf("key=%x salt=%x, want %x/%x", got, gotSalt, key, salt)
	}
}

func TestResolveKeyOrPromptNoSaltErrors(t *testing.T) {
	t.Setenv("ENVER_KEY", "")
	oldKeyFile := crypto.KeyFilePath
	crypto.KeyFilePath = func() string { return filepath.Join(t.TempDir(), "absent") }
	t.Cleanup(func() { crypto.KeyFilePath = oldKeyFile })

	_, _, err := ResolveKeyOrPrompt(Options{}, func() ([]byte, crypto.Argon2Params, string, error) {
		return nil, crypto.Argon2Params{}, "", nil
	})
	if err == nil || !strings.Contains(err.Error(), "no key found") {
		t.Fatalf("expected no-key-found error, got: %v", err)
	}
}

func TestResolveRecovery(t *testing.T) {
	dir := t.TempDir()
	oldKeyFile := crypto.KeyFilePath
	crypto.KeyFilePath = func() string { return filepath.Join(dir, "key") }
	t.Cleanup(func() { crypto.KeyFilePath = oldKeyFile })
	t.Setenv("ENVER_KEY", "")

	salt := []byte("0123456789abcdef")
	pass := "hunter2"
	key, err := crypto.DeriveKey(pass, salt, crypto.CurrentParams)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	enc, err := crypto.EncryptValueWithParams("secret", key, salt, crypto.CurrentParams)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	cfg := config.Config{Profiles: map[string]config.Profile{
		"e": {Env: map[string]string{"API_KEY": enc}},
	}}
	oldPrompt := PromptPassphrase
	oldInteractive := Interactive
	PromptPassphrase = func(prompt string) (string, error) { return pass, nil }
	Interactive = func() bool { return true }
	t.Cleanup(func() {
		PromptPassphrase = oldPrompt
		Interactive = oldInteractive
	})

	r, err := resolve(cfg, "e", Options{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if r.Env["API_KEY"] != "secret" {
		t.Fatalf("decrypted = %q, want secret", r.Env["API_KEY"])
	}
	if _, err := os.Stat(crypto.KeyFilePath()); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
}

func TestResolveRecoveryWrongPassphrase(t *testing.T) {
	dir := t.TempDir()
	oldKeyFile := crypto.KeyFilePath
	crypto.KeyFilePath = func() string { return filepath.Join(dir, "key") }
	t.Cleanup(func() { crypto.KeyFilePath = oldKeyFile })
	t.Setenv("ENVER_KEY", "")

	salt := []byte("0123456789abcdef")
	key, _ := crypto.DeriveKey("right", salt, crypto.CurrentParams)
	enc, _ := crypto.EncryptValueWithParams("secret", key, salt, crypto.CurrentParams)
	cfg := config.Config{Profiles: map[string]config.Profile{
		"e": {Env: map[string]string{"API_KEY": enc}},
	}}
	oldPrompt := PromptPassphrase
	oldInteractive := Interactive
	PromptPassphrase = func(prompt string) (string, error) { return "wrong", nil }
	Interactive = func() bool { return true }
	t.Cleanup(func() {
		PromptPassphrase = oldPrompt
		Interactive = oldInteractive
	})

	if _, err := resolve(cfg, "e", Options{}); err == nil || !strings.Contains(err.Error(), "wrong passphrase") {
		t.Fatalf("expected wrong-passphrase error, got: %v", err)
	}
}

func TestResolveRecoveryNonInteractive(t *testing.T) {
	dir := t.TempDir()
	oldKeyFile := crypto.KeyFilePath
	crypto.KeyFilePath = func() string { return filepath.Join(dir, "key") }
	t.Cleanup(func() { crypto.KeyFilePath = oldKeyFile })
	t.Setenv("ENVER_KEY", "")

	salt := []byte("0123456789abcdef")
	key, _ := crypto.DeriveKey("hunter2", salt, crypto.CurrentParams)
	enc, _ := crypto.EncryptValueWithParams("secret", key, salt, crypto.CurrentParams)
	cfg := config.Config{Profiles: map[string]config.Profile{
		"e": {Env: map[string]string{"API_KEY": enc}},
	}}
	oldInteractive := Interactive
	Interactive = func() bool { return false }
	t.Cleanup(func() { Interactive = oldInteractive })

	if _, err := resolve(cfg, "e", Options{}); err == nil || !strings.Contains(err.Error(), "no key found") {
		t.Fatalf("expected no-key-found error, got: %v", err)
	}
}

func TestResolveForeignEncFailsLoudly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfgContent := "profiles:\n  p:\n    env:\n      TOKEN: enc:v2:YWJj\n"
	if err := os.WriteFile(path, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(cfg, "p", Options{}); err == nil || !strings.Contains(err.Error(), "unsupported encrypted value") {
		t.Fatalf("err = %v, want unsupported encrypted value", err)
	}
}

func TestResolveRecoveryUsesEmbeddedParams(t *testing.T) {
	dir := t.TempDir()
	oldKeyFile := crypto.KeyFilePath
	crypto.KeyFilePath = func() string { return filepath.Join(dir, "key") }
	t.Cleanup(func() { crypto.KeyFilePath = oldKeyFile })
	t.Setenv("ENVER_KEY", "")

	salt := make([]byte, crypto.SaltSize)
	custom := crypto.Argon2Params{Time: 2, Memory: 16 * 1024, Threads: 1}
	key, err := crypto.DeriveKey("hunter2", salt, custom)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.EncryptValueWithParams("secret", key, salt, custom)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Profiles: map[string]config.Profile{
		"p": {Env: map[string]string{"TOKEN": enc}},
	}}
	oldPrompt := PromptPassphrase
	oldInteractive := Interactive
	PromptPassphrase = func(string) (string, error) { return "hunter2", nil }
	Interactive = func() bool { return true }
	t.Cleanup(func() {
		PromptPassphrase = oldPrompt
		Interactive = oldInteractive
	})

	r, err := resolve(cfg, "p", Options{})
	if err != nil {
		t.Fatalf("recovery must derive with embedded params: %v", err)
	}
	if r.Env["TOKEN"] != "secret" {
		t.Fatalf("TOKEN = %q, want secret", r.Env["TOKEN"])
	}
}

func TestResolveDefaultAppliesDefaultAndResolves(t *testing.T) {
	// Create a fixture config with a default profile and a shared value.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgContent := `default: dev
profiles:
  dev:
    env:
      SHARED: dev-value
  prod:
    env:
      SHARED: prod-value
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Empty profile name should resolve to the default profile.
	opts := Options{ConfigPath: cfgPath, NoLocal: true, Name: "enver x"}
	name, r, err := ResolveDefault(opts, "")
	if err != nil {
		t.Fatalf("ResolveDefault with empty profile: %v", err)
	}
	if name != "dev" {
		t.Fatalf("profile = %q, want dev", name)
	}
	if r.Env["SHARED"] != "dev-value" {
		t.Fatalf("env[SHARED] = %q, want dev-value", r.Env["SHARED"])
	}

	// Named profile should resolve directly.
	name, r, err = ResolveDefault(opts, "prod")
	if err != nil {
		t.Fatalf("ResolveDefault with named profile: %v", err)
	}
	if name != "prod" {
		t.Fatalf("profile = %q, want prod", name)
	}
	if r.Env["SHARED"] != "prod-value" {
		t.Fatalf("env[SHARED] = %q, want prod-value", r.Env["SHARED"])
	}

	// Unknown profile must error.
	if _, _, err := ResolveDefault(opts, "missing"); err == nil {
		t.Fatal("ResolveDefault on unknown profile must error")
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestResolveRecoveryConflictingEras(t *testing.T) {
	// Two eras in one profile cannot share a passphrase-derived key; picking a
	// salt by map order would flip recovery between working and failing per
	// run, so Resolve must report the conflict instead.
	dir := t.TempDir()
	oldKeyFile := crypto.KeyFilePath
	crypto.KeyFilePath = func() string { return filepath.Join(dir, "key") }
	t.Cleanup(func() { crypto.KeyFilePath = oldKeyFile })
	t.Setenv("ENVER_KEY", "")

	saltA := []byte("0123456789abcdef")
	saltB := []byte("fedcba9876543210")
	keyA, _ := crypto.DeriveKey("hunter2", saltA, crypto.CurrentParams)
	keyB, _ := crypto.DeriveKey("hunter2", saltB, crypto.CurrentParams)
	encA, _ := crypto.EncryptValueWithParams("a", keyA, saltA, crypto.CurrentParams)
	encB, _ := crypto.EncryptValueWithParams("b", keyB, saltB, crypto.CurrentParams)
	oldPrompt := PromptPassphrase
	oldInteractive := Interactive
	PromptPassphrase = func(prompt string) (string, error) { return "hunter2", nil }
	Interactive = func() bool { return true }
	t.Cleanup(func() {
		PromptPassphrase = oldPrompt
		Interactive = oldInteractive
	})

	cfg := config.Config{Profiles: map[string]config.Profile{
		"e": {Env: map[string]string{"A": encA, "B": encB}},
	}}
	if _, err := resolve(cfg, "e", Options{}); err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("expected era-conflict error, got: %v", err)
	}
}

// TestChildSeesShellValueThroughUnset pins the headline contract end to end:
// the profile suppresses the key, yet the child inherits the shell's live
// value because suppression merely omits it from the overlay.
func TestChildSeesShellValueThroughUnset(t *testing.T) {
	const k = "ENVER_TEST_PASS_THROUGH"
	t.Setenv(k, "shell-live")
	cfg := config.Config{Profiles: map[string]config.Profile{"p": {
		Env:   map[string]string{"OTHER": "x"},
		Unset: config.Unsets{k},
	}}}
	r, err := resolve(cfg, "p", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Env[k]; ok {
		t.Fatalf("resolved env carries the unset key: %v", r.Env)
	}
	seen := 0
	for _, kv := range runner.MergedEnv(osEnvMap(), r.Env) {
		if strings.HasPrefix(kv, k+"=") {
			seen++
			if kv != k+"=shell-live" {
				t.Fatalf("child got %q, want the shell value", kv)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("child env mentions %s %d times, want exactly once with the shell value", k, seen)
	}
}

func TestChdir(t *testing.T) {
	// Anchor a restorable cwd first: t.Chdir undoes any move the test makes.
	t.Chdir(t.TempDir())

	if err := Chdir(""); err != nil {
		t.Fatalf(`Chdir("") = %v, want nil`, err)
	}
	missing := filepath.Join(t.TempDir(), "missing")
	if err := Chdir(missing); err == nil {
		t.Fatalf("Chdir(%q) = nil, want an error", missing)
	}
	dir := t.TempDir()
	if err := Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) = %v, want nil", dir, err)
	}
	if wd, err := os.Getwd(); err != nil || wd != dir {
		t.Fatalf("cwd = %q (err %v), want %q", wd, err, dir)
	}
}

func TestMatchingProfiles(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	cfgContent := "profiles:\n  alpha:\n    env:\n      A: \"1\"\n  beta:\n    env:\n      B: \"2\"\n  delta:\n    extends: [alpha]\n"
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{ConfigPath: cfgPath, NoLocal: true}

	if got := MatchingProfiles(opts, ""); !sliceEq(got, []string{"alpha", "beta", "delta"}) {
		t.Fatalf(`MatchingProfiles("") = %v, want all three`, got)
	}
	if got := MatchingProfiles(opts, "be"); !sliceEq(got, []string{"beta"}) {
		t.Fatalf(`MatchingProfiles("be") = %v, want [beta]`, got)
	}
	// No match: the pre-allocated result slice comes back empty but non-nil;
	// only a load error yields a true nil.
	if got := MatchingProfiles(opts, "zz"); got == nil || len(got) != 0 {
		t.Fatalf(`MatchingProfiles("zz") = %v, want empty non-nil`, got)
	}
	missing := Options{ConfigPath: filepath.Join(dir, "missing.yaml"), NoLocal: true}
	// A missing config file is not an error: load yields an empty Config, so
	// completion comes back empty, not nil.
	if got := MatchingProfiles(missing, ""); got == nil || len(got) != 0 {
		t.Fatalf("MatchingProfiles(missing config) = %v, want empty non-nil", got)
	}
	broken := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(broken, []byte("["), 0o600); err != nil {
		t.Fatal(err)
	}
	// Only a real load error (here: unparseable YAML) yields nil.
	if got := MatchingProfiles(Options{ConfigPath: broken, NoLocal: true}, ""); got != nil {
		t.Fatalf("MatchingProfiles(unparseable config) = %v, want nil", got)
	}
}
