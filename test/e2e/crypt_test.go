package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// requireKeygen runs keygen --random and fails the test on any error.
func requireKeygen(t *testing.T, s *sandbox) {
	t.Helper()
	if r := s.run("keygen", "--random"); r.ExitCode != 0 {
		t.Fatalf("keygen --random: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestKeygenRandomCreatesKeyFile(t *testing.T) {
	s := newSandbox(t)
	requireKeygen(t, s)
	var cache struct {
		Version int    `json:"v"`
		Salt    string `json:"salt"`
		Key     string `json:"key"`
	}
	if err := json.Unmarshal([]byte(s.readFile(s.keyPath())), &cache); err != nil {
		t.Fatalf("key cache must be JSON: %v", err)
	}
	if cache.Version != 1 || cache.Key == "" || cache.Salt == "" {
		t.Fatalf("key cache is incomplete: %+v", cache)
	}
}

func TestEncryptShowDecryptLifecycle(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      TOKEN: plainvalue1\n")
	requireKeygen(t, s)
	r := s.run("encrypt", "p")
	if r.ExitCode != 0 {
		t.Fatalf("encrypt: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(s.readLocal(), "enc:v3:") {
		t.Fatalf("encrypt must write enc:v3 values, got: %q", s.readLocal())
	}
	r = s.run("show", "--no-mask", "p")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "plainvalue1") {
		t.Fatalf("show must decrypt with the default key, got %d, %q, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
	r = s.run("decrypt", "p")
	if r.ExitCode != 0 {
		t.Fatalf("decrypt: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	local := s.readLocal()
	if strings.Contains(local, "enc:v3:") || !strings.Contains(local, "plainvalue1") {
		t.Fatalf("decrypt must restore plaintext, got: %q", local)
	}
}

func TestShowWithoutKeyFailsLoudly(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      TOKEN: plainvalue1\n")
	requireKeygen(t, s)
	if r := s.run("encrypt", "p"); r.ExitCode != 0 {
		t.Fatalf("encrypt failed: %s", r.Stderr)
	}
	if err := os.Remove(s.keyPath()); err != nil {
		t.Fatal(err)
	}
	r := s.run("show", "p")
	if r.ExitCode == 0 {
		t.Fatal("reading encrypted values without any key must fail")
	}
	if !strings.Contains(r.Stderr, "keygen") {
		t.Fatalf("the failure should point at recovery, got: %q", r.Stderr)
	}
}

func TestShowWithWrongKeyFails(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      TOKEN: plainvalue1\n")
	requireKeygen(t, s)
	if r := s.run("encrypt", "p"); r.ExitCode != 0 {
		t.Fatalf("encrypt failed: %s", r.Stderr)
	}
	if r := s.run("keygen", "--random", "--force"); r.ExitCode != 0 {
		t.Fatalf("key rotation failed: %s", r.Stderr)
	}
	r := s.run("show", "p")
	if r.ExitCode == 0 {
		t.Fatal("a foreign key must not decrypt the values")
	}
	if !strings.Contains(r.Stderr, "decrypt") {
		t.Fatalf("the failure should name the decrypt step, got: %q", r.Stderr)
	}
}

func TestEnverKeyEnvDecrypts(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      TOKEN: plainvalue1\n")
	requireKeygen(t, s)
	if r := s.run("encrypt", "p"); r.ExitCode != 0 {
		t.Fatalf("encrypt failed: %s", r.Stderr)
	}
	var cache struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(s.readFile(s.keyPath())), &cache); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.keyPath()); err != nil {
		t.Fatal(err)
	}
	s.setEnv("ENVER_KEY", cache.Key)
	r := s.run("show", "--no-mask", "p")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "plainvalue1") {
		t.Fatalf("ENVER_KEY must decrypt, got %d, %q, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}
