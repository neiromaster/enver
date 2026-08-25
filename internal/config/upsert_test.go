package config

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/crypto"
)

func TestUpsertPreservesComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `# top-level comment
default: anth

profiles:
  # endpoint for anthropic
  anth:
    env:
      ANTHROPIC_API_KEY: sk-ant-xxx
      ANTHROPIC_MODEL: claude-sonnet-5
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	p := Profile{Env: map[string]string{"ANTHROPIC_BASE_URL": "https://api.anthropic.com"}}
	if err := UpsertProfile(path, "anth", p, false, false); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "# top-level comment") {
		t.Error("top-level comment lost")
	}
	if !strings.Contains(s, "# endpoint for anthropic") {
		t.Error("inline profile comment lost")
	}
	if !strings.Contains(s, "ANTHROPIC_BASE_URL: https://api.anthropic.com") {
		t.Error("new env key not written")
	}
	if !strings.Contains(s, "ANTHROPIC_API_KEY: sk-ant-xxx") {
		t.Error("existing env key lost")
	}
}

func TestUpsertCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "config.yaml")
	p := Profile{Extends: Extends{"anth"}, Env: map[string]string{"K": "v"}}
	if err := UpsertProfile(path, "new", p, true, false); err != nil {
		t.Fatalf("upsert into missing file: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "new:") || !strings.Contains(s, "extends: anth") {
		t.Fatalf("created file missing expected content:\n%s", s)
	}
	if !strings.Contains(s, "default: new") {
		t.Fatalf("default not set when requested:\n%s", s)
	}
}

func TestEncryptDecryptFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := `default: anth
profiles:
  anth:
    env:
      ANTHROPIC_API_KEY: sk-ant-secret-1234567890
      ANTHROPIC_MODEL: claude-sonnet-5
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}

	salt := []byte("0123456789abcdef")
	n, err := EncryptFile(path, key, salt, "", false)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if n != 1 {
		t.Fatalf("encrypted %d values, want 1 (only API_KEY is secret-looking)", n)
	}
	enc, _ := os.ReadFile(path)
	if !strings.Contains(string(enc), "enc:v3:") {
		t.Fatal("encrypted value not found in file or lacks enc:v3: prefix")
	}
	if !strings.Contains(string(enc), "claude-sonnet-5") {
		t.Fatal("non-secret value got encrypted")
	}

	// idempotent
	n2, err := EncryptFile(path, key, salt, "", false)
	if err != nil {
		t.Fatalf("re-encrypt: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("re-encrypt encrypted %d, want 0 (idempotent)", n2)
	}

	// decrypt restores plaintext
	n3, err := DecryptFile(path, key, "")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if n3 != 1 {
		t.Fatalf("decrypted %d, want 1", n3)
	}
	dec, _ := os.ReadFile(path)
	if !strings.Contains(string(dec), "sk-ant-secret-1234567890") {
		t.Fatal("decrypted plaintext not restored")
	}
}

func TestEncryptFileEncodesURLSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// The fixture intentionally carries URL credentials to prove they encrypt.
	//nolint:gosec
	original := `profiles:
  prod:
    env:
      DATABASE_URL: postgres://user:pass@db.internal:5432/app
      ANTHROPIC_BASE_URL: https://api.anthropic.com
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	salt := []byte("0123456789abcdef")

	n, err := EncryptFile(path, key, salt, "", false)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if n != 1 {
		t.Fatalf("encrypted %d values, want 1 (only DATABASE_URL carries credentials)", n)
	}
	enc, _ := os.ReadFile(path)
	if !strings.Contains(string(enc), "enc:v3:") {
		t.Fatal("DATABASE_URL not encrypted")
	}
	if strings.Contains(string(enc), "user:pass@db.internal") {
		t.Fatal("URL credentials leaked into the encrypted file")
	}
	if !strings.Contains(string(enc), "https://api.anthropic.com") {
		t.Fatal("plain URL without credentials got encrypted")
	}

	n3, err := DecryptFile(path, key, "")
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if n3 != 1 {
		t.Fatalf("decrypted %d, want 1", n3)
	}
	dec, _ := os.ReadFile(path)
	if !strings.Contains(string(dec), "postgres://user:pass@db.internal:5432/app") {
		t.Fatal("URL credentials not restored on decrypt")
	}
}

func TestEncryptFileWrongKeyFailsDecrypt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      API_KEY: secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	keyA := make([]byte, 32)
	keyB := make([]byte, 32)
	keyB[0] = 1
	if _, err := EncryptFile(path, keyA, []byte("0123456789abcdef"), "", false); err != nil {
		t.Fatal(err)
	}
	if _, err := DecryptFile(path, keyB, ""); err == nil {
		t.Fatal("decrypt with wrong key should fail")
	}
}

func TestCryptoPrefixMatch(t *testing.T) {
	if crypto.IsEncrypted("enc:v2:YWJj") {
		t.Fatal("IsEncrypted must not match foreign enc: prefixes")
	}
}

func TestUpsertWritesEnvCommentAboveEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	p := Profile{Env: map[string]string{"API_KEY": "sk-xxx"}, Comments: map[string]string{"API_KEY": "get this token from vault X"}}
	if err := UpsertProfile(path, "anth", p, false, false); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	s := string(mustRead(t, path))
	// Comment renders, indented, on its own line immediately above the entry,
	// regardless of yaml.v3's indent width or key-vs-value attachment.
	re := regexp.MustCompile(`(?m)^[ \t]*# get this token from vault X[ \t]*\n[ \t]*API_KEY:`)
	if !re.MatchString(s) {
		t.Fatalf("comment not rendered above API_KEY entry:\n%s", s)
	}
}

func TestUpsertKeepsCommentWhenValueUpdatedWithoutComment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := UpsertProfile(path, "anth",
		Profile{Env: map[string]string{"API_KEY": "v1"}, Comments: map[string]string{"API_KEY": "from vault"}},
		false, false); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	// Re-upsert the same key with a new value but no comment: value changes,
	// the existing comment must survive.
	if err := UpsertProfile(path, "anth",
		Profile{Env: map[string]string{"API_KEY": "v2"}},
		false, false); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	s := string(mustRead(t, path))
	if !strings.Contains(s, "API_KEY: v2") {
		t.Fatalf("value not updated:\n%s", s)
	}
	if !strings.Contains(s, "from vault") {
		t.Fatalf("existing comment wiped on re-upsert:\n%s", s)
	}
}

func TestEnvCommentSurvivesEncryptDecrypt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := UpsertProfile(path, "anth",
		Profile{Env: map[string]string{"API_KEY": "sk-secret"}, Comments: map[string]string{"API_KEY": "from vault"}},
		false, false); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	if _, err := EncryptFile(path, key, []byte("0123456789abcdef"), "", false); err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	enc := string(mustRead(t, path))
	if !strings.Contains(enc, "from vault") {
		t.Fatalf("comment lost after encrypt:\n%s", enc)
	}
	if _, err := DecryptFile(path, key, ""); err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	dec := string(mustRead(t, path))
	if !strings.Contains(dec, "from vault") {
		t.Fatalf("comment lost after decrypt:\n%s", dec)
	}
}

func TestUpsertForceExtendsClearsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := UpsertProfile(path, "p", Profile{Extends: Extends{"base"}, Env: map[string]string{"A": "1"}}, false, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := UpsertProfile(path, "p", Profile{Env: map[string]string{"B": "2"}}, false, true); err != nil {
		t.Fatalf("forceExtends clear: %v", err)
	}
	prof, _, _, _, err := ReadProfile(path, "p")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(prof.Extends) > 0 {
		t.Errorf("Extends = %q, want empty (forceExtends cleared it)", prof.Extends)
	}
	if prof.Env["A"] != "1" || prof.Env["B"] != "2" {
		t.Errorf("env should merge to {A:1, B:2}, got %+v", prof.Env)
	}
}

func TestUpsertPreserveExtendsKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := UpsertProfile(path, "p", Profile{Extends: Extends{"base"}, Env: map[string]string{"A": "1"}}, false, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := UpsertProfile(path, "p", Profile{Env: map[string]string{"B": "2"}}, false, false); err != nil {
		t.Fatalf("preserve: %v", err)
	}
	prof, _, _, _, err := ReadProfile(path, "p")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !prof.Extends.Has("base") {
		t.Errorf("Extends = %q, want base (forceExtends=false preserves it)", prof.Extends)
	}
	if prof.Env["A"] != "1" || prof.Env["B"] != "2" {
		t.Errorf("env should merge to {A:1, B:2}, got %+v", prof.Env)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFirstSaltAndSample(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	key := make([]byte, 32)
	salt := []byte("0123456789abcdef")
	enc, err := crypto.EncryptValue("secret", key, salt)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// No encrypted values.
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      A: \"1\"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	gotSalt, _, gotSample, err := FirstSaltAndSample(path)
	if err != nil || gotSalt != nil || gotSample != "" {
		t.Fatalf("plain config: salt=%v sample=%q err=%v, want none", gotSalt, gotSample, err)
	}
	// One v3 value.
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      A: \""+enc+"\"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	gotSalt, _, gotSample, err = FirstSaltAndSample(path)
	if err != nil || string(gotSalt) != string(salt) || gotSample != enc {
		t.Fatalf("v3 config: salt=%q sample=%q err=%v, want %q/%q", gotSalt, gotSample, err, salt, enc)
	}
	// Missing file → error.
	if _, _, _, err := FirstSaltAndSample(filepath.Join(dir, "nope.yaml")); err == nil {
		t.Fatal("missing file must error")
	}
}

func TestEncryptFileSharesOneSaltPerRun(t *testing.T) {
	// F3 regression: every value encrypted in one EncryptFile run shares the
	// caller salt; per-value salts would strand all but the first value for
	// passphrase recovery.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "default: anth\nprofiles:\n  anth:\n    env:\n      SECRET_1: value-one\n      SECRET_2: value-two\n      SECRET_3: value-three\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	salt := []byte("0123456789abcdef")
	n, err := EncryptFile(path, key, salt, "", true)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if n != 3 {
		t.Fatalf("encrypted %d values, want 3", n)
	}
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range c.Profiles["anth"].Env {
		got, _, err := crypto.SaltFromValue(v)
		if err != nil {
			t.Fatalf("%s: SaltFromValue: %v", k, err)
		}
		if !bytes.Equal(got, salt) {
			t.Errorf("%s has salt %x, want %x (all values must share one salt)", k, got, salt)
		}
	}
}

func TestEncryptFileSaltStableAcrossRuns(t *testing.T) {
	// F3 regression across runs: the CLI passes the key-cache salt, which is
	// stable between invocations; a second run must reuse it, not mint a new
	// one per value.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	original := "default: anth\nprofiles:\n  anth:\n    env:\n      EXISTING_SECRET: existing-value\n      OTHER_VALUE: new-value-to-encrypt\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	salt := []byte("0123456789abcdef")
	n1, err := EncryptFile(path, key, salt, "", false)
	if err != nil {
		t.Fatalf("first encrypt: %v", err)
	}
	if n1 != 1 {
		t.Fatalf("first encrypt encrypted %d values, want 1 (only EXISTING_SECRET is secret-looking)", n1)
	}
	n2, err := EncryptFile(path, key, salt, "", true)
	if err != nil {
		t.Fatalf("second encrypt: %v", err)
	}
	if n2 != 1 {
		t.Fatalf("second encrypt encrypted %d values, want 1", n2)
	}
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range c.Profiles["anth"].Env {
		got, _, err := crypto.SaltFromValue(v)
		if err != nil {
			t.Fatalf("%s: SaltFromValue: %v", k, err)
		}
		if !bytes.Equal(got, salt) {
			t.Errorf("%s has salt %x, want %x (runs must share the caller salt)", k, got, salt)
		}
	}
}

func TestEncryptFileReusesFileKDFParams(t *testing.T) {
	// Recovery derives the key from any value's header, so a new value must
	// carry the params of the era its key actually came from, not what the
	// current build would mint.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	key := make([]byte, 32)
	salt := []byte("0123456789abcdef")
	custom := crypto.Argon2Params{Time: 2, Memory: 16 * 1024, Threads: 1}
	enc, err := crypto.EncryptValueWithParams("existing", key, salt, custom)
	if err != nil {
		t.Fatal(err)
	}
	cfg := "profiles:\n  p:\n    env:\n      TOKEN: \"" + enc + "\"\n      SECRET: new-secret\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	n, err := EncryptFile(path, key, salt, "", true)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if n != 1 {
		t.Fatalf("encrypted %d values, want 1", n)
	}
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := crypto.SaltFromValue(c.Profiles["p"].Env["SECRET"])
	if err != nil {
		t.Fatalf("SaltFromValue: %v", err)
	}
	if params != custom {
		t.Fatalf("new value params = %+v, want the file's %+v", params, custom)
	}

	// A run with a different salt is refused: it would mix two keys and
	// strand the value already encrypted under the file's key.
	other := filepath.Join(dir, "other.yaml")
	if err := os.WriteFile(other, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EncryptFile(other, key, []byte("fedcba9876543210"), "", true); err == nil ||
		!strings.Contains(err.Error(), "different key") {
		t.Fatalf("encrypt with other salt: err = %v, want refusal", err)
	}
}

func TestEncryptDecryptFileForeignEncAnyProfile(t *testing.T) {
	// Foreign enc: values fail loudly even when the command targets another
	// profile: the file as a whole is unreadable by this build, and a scoped
	// success would leave the impression it was handled.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfg := "profiles:\n  a:\n    env:\n      TOKEN: enc:v2:YWJj\n  b:\n    env:\n      PLAIN: value\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	salt := []byte("0123456789abcdef")
	if _, err := EncryptFile(path, key, salt, "b", true); err == nil || !strings.Contains(err.Error(), "unsupported encrypted value") {
		t.Fatalf("EncryptFile(b) must reject foreign enc: in profile a, got: %v", err)
	}
	if _, err := DecryptFile(path, key, "b"); err == nil || !strings.Contains(err.Error(), "unsupported encrypted value") {
		t.Fatalf("DecryptFile(b) must reject foreign enc: in profile a, got: %v", err)
	}
}

func TestEncryptFileMalformedEncV3Fails(t *testing.T) {
	// A truncated enc:v3 value must not ride the idempotence branch: encrypt
	// reports the parse error instead of a silent success that leaves the file
	// broken for the next resolve.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      TOKEN: \"enc:v3:\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	salt := []byte("0123456789abcdef")
	if _, err := EncryptFile(path, key, salt, "", true); err == nil || !strings.Contains(err.Error(), "malformed enc:v3 header") {
		t.Fatalf("EncryptFile must reject malformed enc:v3 value, got: %v", err)
	}
}

func TestFirstSaltAndSampleForeignEnc(t *testing.T) {
	// A v2-only config must surface the unsupported-value error, not "no key
	// found": the keygen advice would be a dead end.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  p:\n    env:\n      TOKEN: enc:v2:YWJj\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := FirstSaltAndSample(path); err == nil || !strings.Contains(err.Error(), "unsupported encrypted value") {
		t.Fatalf("FirstSaltAndSample must reject foreign enc: values, got: %v", err)
	}
}

func TestFirstSaltAndSampleConflictingSalts(t *testing.T) {
	// Two eras in one file cannot share one passphrase-derived key; sampling
	// one at random would make recovery nondeterministic, so it is an error.
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	key := make([]byte, 32)
	a, err := crypto.EncryptValue("a", key, []byte("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := crypto.EncryptValue("b", key, []byte("fedcba9876543210"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := "profiles:\n  p:\n    env:\n      A: \"" + a + "\"\n      B: \"" + b + "\"\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := FirstSaltAndSample(path); err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("FirstSaltAndSample must reject conflicting salts, got: %v", err)
	}
}
