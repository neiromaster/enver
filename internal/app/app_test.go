package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
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
	env, chain, err := Resolve(cfg, "p", Options{})
	if err != nil {
		t.Fatalf("plaintext resolve: %v", err)
	}
	if env["MODEL"] != "claude-sonnet-5" {
		t.Fatalf("env = %v", env)
	}
	if len(chain) != 1 || chain[0] != "p" {
		t.Fatalf("chain = %v", chain)
	}

	// An encrypted profile with no key available → error. Encrypt with a real
	// key file so the later decrypt step can reuse the same file.
	dir := t.TempDir()
	kpath := filepath.Join(dir, "key")
	if err := crypto.GenerateKey(kpath, true); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.LoadKey(kpath)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.EncryptValue("secret-value", key)
	if err != nil {
		t.Fatal(err)
	}
	encCfg := config.Config{Profiles: map[string]config.Profile{
		"e": {Env: map[string]string{"API_KEY": enc}},
	}}
	if _, _, err := Resolve(encCfg, "e", Options{KeyPath: "/nonexistent/key"}); err == nil {
		t.Fatal("encrypted profile without key should error")
	}

	// Same profile with the right key file → decrypts.
	env2, _, err := Resolve(encCfg, "e", Options{KeyPath: kpath})
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if env2["API_KEY"] != "secret-value" {
		t.Fatalf("decrypted = %q, want secret-value", env2["API_KEY"])
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
