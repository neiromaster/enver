package remote

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
)

// gateSeal produces current-era ciphertext under the given KDF parameters. The
// gate never decrypts, so any key and salt serve; the parameters are the
// variable under test.
func gateSeal(t *testing.T, p crypto.Argon2Params) string {
	t.Helper()
	enc, err := crypto.EncryptValueWithParams("secret", make([]byte, 32), make([]byte, crypto.SaltSize), p)
	if err != nil {
		t.Fatalf("seal test value with %+v: %v", p, err)
	}
	return enc
}

// gateEnvFile renders one profile whose env holds pairs in order.
func gateEnvFile(profile string, pairs ...string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "profiles:\n  %q:\n    env:\n", profile)
	for i := 0; i+1 < len(pairs); i += 2 {
		fmt.Fprintf(&b, "      %q: %q\n", pairs[i], pairs[i+1])
	}
	return []byte(b.String())
}

func gateWarning(warnings []string, parts ...string) bool {
	for _, w := range warnings {
		matches := true
		for _, p := range parts {
			if !strings.Contains(w, p) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func gateRequireWarning(t *testing.T, warnings []string, parts ...string) {
	t.Helper()
	if !gateWarning(warnings, parts...) {
		t.Fatalf("warnings %q do not carry %q", warnings, parts)
	}
}

func gateRequireError(t *testing.T, err error, parts ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("Gate returned no error, want one carrying %q", parts)
	}
	for _, p := range parts {
		if !strings.Contains(err.Error(), p) {
			t.Fatalf("Gate error %q does not carry %q", err, p)
		}
	}
}

func TestGateAcceptsPlainContent(t *testing.T) {
	content := []byte(`profiles:
  ollama:
    env:
      OLLAMA_HOST: http://localhost:11434
  qwen:
    extends: ollama
    env:
      MODEL: qwen2.5-coder:32b
`)
	warnings, err := Gate(content, GateOpts{Record: "llm"})
	if err != nil {
		t.Fatalf("Gate refused a valid remote: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("Gate warnings = %q, want none", warnings)
	}
}

func TestGateRejectsUnparseableYAML(t *testing.T) {
	_, err := Gate([]byte("profiles:\n  a: [unclosed\n"), GateOpts{Record: "llm"})
	gateRequireError(t, err, "YAML")
}

func TestGateRejectsInvalidEnvNames(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
	}{
		{"shell metacharacters in an env key", gateEnvFile("a", "EVIL;rm -rf /", "x")},
		{"invalid unset entry", []byte("profiles:\n  a:\n    unset: [\"BAD-KEY\"]\n    env:\n      K: v\n")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warnings, err := Gate(tc.content, GateOpts{Record: "llm"})
			gateRequireError(t, err, "invalid env key name")
			if len(warnings) != 0 {
				t.Fatalf("a refused pull returned warnings %q", warnings)
			}
		})
	}
}

func TestGateRejectsUpstreamProfileNames(t *testing.T) {
	cases := []struct {
		name    string
		profile string
	}{
		{"slash belongs to the record prefix", "a/b"},
		{"empty name", ""},
		{"control character", "a\u0001b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Gate(gateEnvFile(tc.profile, "K", "v"), GateOpts{Record: "llm"})
			gateRequireError(t, err, "bare upstream profile name")
			if strings.ContainsRune(err.Error(), '\u0001') {
				t.Fatalf("error carries the raw control character: %q", err)
			}
		})
	}
	t.Run("long name truncates on a rune boundary", func(t *testing.T) {
		name := strings.Repeat("a", 47) + "é"
		_, err := Gate(gateEnvFile(name, "K", "v"), GateOpts{Record: "llm"})
		gateRequireError(t, err, "bare upstream profile name")
		if strings.Contains(err.Error(), `\xc3`) || strings.Contains(err.Error(), `\xa9`) {
			t.Fatalf("truncation split a rune: %q", err)
		}
	})
}

// Strict scoping: a fetched file resolves only what its own namespace defines —
// its own profiles and the records its own remotes: section declares. Everything
// else is refused before it can bind whatever the pulling user happens to have:
// a user profile, another record's projection, the record's own pulled name, or
// a name nothing in the file can denote.
func TestGateRejectsOutOfNamespaceExtends(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"a bare name the file does not define", "nowhere"},
		{"a user profile", "provider"},
		{"another record's projection", "other/base"},
		{"the pulled record's own name", "llm/base"},
		{"an undeclared own-record spelling", "llm/missing"},
		{"a deeper path under no record", "a/b/c"},
		{"a leading slash", "/base"},
		{"a space", "my profile"},
		{"a control character", "a\u0001b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			content := fmt.Appendf(nil, "profiles:\n  base:\n    env:\n      K: v\n  child:\n    extends: %q\n    env:\n      J: w\n", tc.target)
			_, err := Gate(content, GateOpts{Record: "llm"})
			gateRequireError(t, err, "outside this file", "child", quoteName(tc.target))
			if strings.ContainsRune(err.Error(), '\u0001') {
				t.Fatalf("error carries the raw control character: %q", err)
			}
		})
	}
	t.Run("mapping instead of a name", func(t *testing.T) {
		content := []byte("profiles:\n  child:\n    extends:\n      parent: reason\n    env:\n      J: w\n")
		_, err := Gate(content, GateOpts{Record: "llm"})
		gateRequireError(t, err, "not a valid config")
	})
	t.Run("no-prefix does not widen the namespace", func(t *testing.T) {
		content := []byte("profiles:\n  a:\n    extends: llm/b\n    env:\n      K: v\n  b:\n    env:\n      J: w\n")
		_, err := Gate(content, GateOpts{Record: "llm", NoPrefix: true})
		gateRequireError(t, err, "outside this file", quoteName("llm/b"))
	})
}

func TestGateRejectsInFileExtendsCycles(t *testing.T) {
	cases := map[string]string{
		"self": "profiles:\n  a:\n    extends: a\n    env:\n      K: v\n",
		"mutual": `profiles:
  a:
    extends: b
    env:
      K: v
  b:
    extends: a
    env:
      J: w
`,
		"through a parent": `profiles:
  a:
    extends: b
    env:
      K: v
  b:
    extends: c
    env:
      J: w
  c:
    extends: a
    env:
      L: x
`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Gate([]byte(content), GateOpts{Record: "llm"})
			if !errors.Is(err, config.ErrExtendsCycle) {
				t.Fatalf("Gate error = %v, want config.ErrExtendsCycle", err)
			}
		})
	}
}

// A declared child is inside the file's namespace: extending record/profile
// while the file's own remotes: section declares that record is the composition
// the namespace rule admits, and it draws no refusal. The remotes: section still
// draws its advisory until nesting makes the section real, so that notice is the
// only warning a declared-child pull may carry.
func TestGateAcceptsDeclaredChildExtends(t *testing.T) {
	content := []byte(`profiles:
  qwen:
    extends: claude/default
    env:
      MODEL: qwen
remotes:
  claude:
    url: https://example.com/claude.yaml
`)
	warnings, err := Gate(content, GateOpts{Record: "llm"})
	if err != nil {
		t.Fatalf("Gate refused a declared child target: %v", err)
	}
	if len(warnings) != 1 || !gateWarning(warnings, "declares a remotes: section") {
		t.Fatalf("warnings = %q, want only the remotes-section advisory", warnings)
	}
}

func TestGateRejectsForeignEraCiphertext(t *testing.T) {
	current := gateSeal(t, crypto.CurrentParams)
	cases := []struct {
		name  string
		pairs []string
		want  []string
	}{
		{"v1", []string{"TOKEN", "enc:v1:YWJj"}, []string{"cannot read", "enc:v1:"}},
		{"v2", []string{"TOKEN", "enc:v2:YWJj"}, []string{"cannot read", "enc:v2:"}},
		{"two foreign eras", []string{"TOKEN", "enc:v1:YWJj", "API_KEY", "enc:v2:YWJj"}, []string{"cannot read", "enc:v1:", "enc:v2:"}},
		{"mixed eras", []string{"TOKEN", "enc:v2:YWJj", "API_KEY", current}, []string{"mixes ciphertext eras", "enc:v2:", "enc:v3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Gate(gateEnvFile("a", tc.pairs...), GateOpts{Record: "llm"})
			gateRequireError(t, err, tc.want...)
		})
	}
}

func TestGateRejectsEncryptedValueCount(t *testing.T) {
	sealed := gateSeal(t, crypto.CurrentParams)
	pairs := func(n int) []string {
		out := make([]string, 0, 2*n)
		for i := range n {
			out = append(out, fmt.Sprintf("K%d", i), sealed)
		}
		return out
	}
	_, err := Gate(gateEnvFile("a", pairs(101)...), GateOpts{Record: "llm"})
	gateRequireError(t, err, "101", "at most 100")

	warnings, err := Gate(gateEnvFile("a", pairs(100)...), GateOpts{Record: "llm"})
	if err != nil {
		t.Fatalf("Gate refused exactly 100 encrypted values: %v", err)
	}
	gateRequireWarning(t, warnings, "K99")
}

func TestGateChecksKDFParams(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		wantErr  []string
		wantWarn []string
	}{
		{"current parameters stay quiet", gateSeal(t, crypto.CurrentParams), nil, nil},
		{"above the defaults warns", gateSeal(t, crypto.Argon2Params{Time: 4, Memory: 65536, Threads: 4}), nil, []string{"KDF parameters above"}},
		{"at the parse caps warns", gateSeal(t, crypto.Argon2Params{Time: 32, Memory: 98304, Threads: 4}), nil, []string{"KDF parameters above"}},
		{"only parallelism above the default warns", gateSeal(t, crypto.Argon2Params{Time: 3, Memory: 65536, Threads: 8}), nil, []string{"KDF parameters above", "a higher t or m makes every resolve cost more"}},
		{"over the time cap", "enc:v3:argon2id:33:65536:4:AAAA", []string{"must be <= 32"}, nil},
		{"over the memory cap", "enc:v3:argon2id:3:1048577:4:AAAA", []string{"must be <= 1048576"}, nil},
		{"over the thread cap", "enc:v3:argon2id:3:65536:33:AAAA", []string{"must be <= 32"}, nil},
		{"over the cost cap", "enc:v3:argon2id:32:98305:4:AAAA", []string{"t*m"}, nil},
		{"payload too short to hold a salt", "enc:v3:argon2id:3:65536:4:AAAA", []string{"ciphertext too short"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			warnings, err := Gate(gateEnvFile("a", "TOKEN", tc.value), GateOpts{Record: "llm"})
			if tc.wantErr != nil {
				gateRequireError(t, err, tc.wantErr...)
				return
			}
			if err != nil {
				t.Fatalf("Gate refused a readable value: %v", err)
			}
			if tc.wantWarn != nil {
				gateRequireWarning(t, warnings, tc.wantWarn...)
				return
			}
			for _, w := range warnings {
				if strings.Contains(w, "KDF parameters") {
					t.Fatalf("current parameters raised %q", w)
				}
			}
		})
	}
}

func TestGateWarnsNestedRemotes(t *testing.T) {
	content := []byte(`profiles:
  a:
    env:
      K: v
remotes:
  llm:
    url: https://example.com/x.yaml
`)
	warnings, err := Gate(content, GateOpts{Record: "llm"})
	if err != nil {
		t.Fatalf("Gate refused content with a nested remotes section: %v", err)
	}
	gateRequireWarning(t, warnings, "remotes:", "ignored")
}

func TestGateRejectsInvalidNestedRecordName(t *testing.T) {
	content := []byte(`profiles:
  a:
    env:
      K: v
remotes:
  a/b:
    url: https://example.com/x.yaml
`)
	_, err := Gate(content, GateOpts{Record: "llm"})
	gateRequireError(t, err, "invalid record name")
}

func TestGateWarnsNoPrefixCollisions(t *testing.T) {
	content := []byte(`profiles:
  dev:
    env:
      A: one
  prod:
    env:
      B: two
  extra:
    env:
      C: three
`)
	merged := config.Config{
		Profiles: map[string]config.Profile{
			"dev":  {Env: map[string]string{"A": "mine"}},
			"prod": {Env: map[string]string{"B": "mine"}},
		},
		ProfileLayer: map[string]string{"dev": config.LayerLocal, "prod": config.LayerGlobal},
	}
	warnings, err := Gate(content, GateOpts{Record: "llm", NoPrefix: true, Merged: merged})
	if err != nil {
		t.Fatalf("Gate refused a colliding no-prefix pull: %v", err)
	}
	gateRequireWarning(t, warnings, `"dev"`, `"prod"`, "overridden")
	joined := strings.Join(warnings, "\n")
	if strings.Contains(joined, "extra") {
		t.Fatalf("warning names a non-colliding profile: %q", joined)
	}

	warnings, err = Gate(content, GateOpts{Record: "llm", Merged: merged})
	if err != nil {
		t.Fatalf("Gate refused a prefixed pull: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("a prefixed pull warned about collisions: %q", warnings)
	}
}

func TestGateAcceptsCurrentEraCiphertext(t *testing.T) {
	sealed := gateSeal(t, crypto.CurrentParams)
	content := []byte(fmt.Sprintf(`profiles:
  base:
    env:
      TOKEN: %q
  child:
    extends: base
    env:
      API_KEY: %q
`, sealed, sealed))
	warnings, err := Gate(content, GateOpts{Record: "llm"})
	if err != nil {
		t.Fatalf("Gate refused current-era ciphertext: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %q, want exactly the encrypted-values warning", warnings)
	}
	gateRequireWarning(t, warnings,
		"base/TOKEN",
		"child/API_KEY",
		"patch these keys with plaintext values while foreign ciphertext remains, and encrypt only after every foreign value is replaced - your ciphertext beside the author's puts two salts in one profile and breaks passphrase recovery",
	)
}

func TestGateRejectsAliasFlood(t *testing.T) {
	flood := func(aliases int) []byte {
		var b strings.Builder
		b.WriteString("profiles: {}\ndummy:\n  - &a x\n")
		for range aliases {
			b.WriteString("  - *a\n")
		}
		return []byte(b.String())
	}
	_, err := Gate(flood(1001), GateOpts{Record: "llm"})
	gateRequireError(t, err, "anchors and aliases")

	if _, err := Gate(flood(999), GateOpts{Record: "llm"}); err != nil {
		t.Fatalf("Gate refused exactly 1000 anchors and aliases: %v", err)
	}
}
