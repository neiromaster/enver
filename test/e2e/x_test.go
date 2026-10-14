package e2e

import (
	"encoding/base64"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// childPrinter returns a child command that prints the named env var raw
// (no trailing newline beyond the child's own): sh on POSIX, cmd on windows.
// Together with childAbsentProbe these are the only GOOS branches the suite
// is allowed.
func childPrinter(varName string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo %" + varName + "%"}
	}
	return []string{"sh", "-c", `printf %s "$` + varName + `"`}
}

// childAbsentProbe prints absent only when the named var is empty or unset
// in the child env: a fence must leave nothing behind to detect.
func childAbsentProbe(varName string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "if not defined " + varName + " echo absent"}
	}
	return []string{"sh", "-c", `test -z "$` + varName + `" && echo absent`}
}

func TestXPropagatesChildExitCode(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      A: \"1\"\n")
	var child []string
	if runtime.GOOS == "windows" {
		child = []string{"cmd", "/c", "exit", "3"}
	} else {
		child = []string{"sh", "-c", "exit 3"}
	}
	r := s.run(append([]string{"x", "p", "--"}, child...)...)
	if r.ExitCode != 3 {
		t.Fatalf("enver x must propagate the child exit code, got %d, stderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestXPassesResolvedEnvToChild(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      GREETING: hello\n")
	r := s.run(append([]string{"x", "p", "--"}, childPrinter("GREETING")...)...)
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if got := strings.TrimSpace(r.Stdout); got != "hello" {
		t.Fatalf("child must see the resolved env, got %q (stderr: %s)", got, r.Stderr)
	}
}

func TestXUsesDefaultProfile(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      A: \"1\"\n")
	if r := s.run("default", "p"); r.ExitCode != 0 {
		t.Fatalf("default set failed: %s", r.Stderr)
	}
	r := s.run(append([]string{"x", "--"}, childPrinter("A")...)...)
	if r.ExitCode != 0 || strings.TrimSpace(r.Stdout) != "1" {
		t.Fatalf("x without a profile must use the default, got %d, %q", r.ExitCode, r.Stdout)
	}
}

func TestXInterpolatesEnvReferences(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      RAW: $HOME\n")
	r := s.run(append([]string{"x", "p", "--"}, childPrinter("RAW")...)...)
	if r.ExitCode != 0 || strings.TrimSpace(r.Stdout) != s.home {
		t.Fatalf("$HOME must expand to the sandbox home, got %q (want %q)", r.Stdout, s.home)
	}
}

func TestXNoExpandKeepsLiteral(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      RAW: $HOME\n")
	r := s.run(append([]string{"--no-expand", "x", "p", "--"}, childPrinter("RAW")...)...)
	if r.ExitCode != 0 || strings.TrimSpace(r.Stdout) != "$HOME" {
		t.Fatalf("--no-expand must keep the literal, got %q", r.Stdout)
	}
}

func TestXFenceHidesUnsetFromChild(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    unset: [SECRET]\n    env:\n      SECRET: s\n")
	r := s.run(append([]string{"x", "p", "--"}, childAbsentProbe("SECRET")...)...)
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "absent") {
		t.Fatalf("a fenced var must not reach the child, got %q (stderr: %s)", r.Stdout, r.Stderr)
	}
}

func TestXInheritedUnsetStillFences(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  base:\n    env:\n      A: from-base\n  leaf:\n    extends: [base]\n    unset: [A]\n")
	r := s.run(append([]string{"x", "leaf", "--"}, childAbsentProbe("A")...)...)
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "absent") {
		t.Fatalf("a fence inherited along the chain must stay carried to the child, got %q (stderr: %s)", r.Stdout, r.Stderr)
	}
}

func TestXChildRedefinitionBeatsInheritedUnset(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  base:\n    env:\n      A: from-base\n  mid:\n    extends: [base]\n    unset: [A]\n  leaf:\n    extends: [mid]\n    env:\n      A: from-leaf\n")
	r := s.run(append([]string{"x", "leaf", "--"}, childPrinter("A")...)...)
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if got := strings.TrimSpace(r.Stdout); got != "from-leaf" {
		t.Fatalf("a closer redefinition must override the inherited unset, got %q (stderr: %s)", got, r.Stderr)
	}
}

func TestXStripsEnverKeyFromChild(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      A: \"1\"\n")
	s.setEnv("ENVER_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	r := s.run(append([]string{"x", "p", "--"}, childAbsentProbe("ENVER_KEY")...)...)
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "absent") {
		t.Fatalf("ENVER_KEY must not reach the child, got %d, %q, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "ENVER_FORWARD_KEY=1 to forward it") {
		t.Fatalf("stripping a set key must print the forwarding hint, stderr: %s", r.Stderr)
	}
}

func TestXForwardKeyMaterializesKeyFileKey(t *testing.T) {
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
	s.setEnv("ENVER_FORWARD_KEY", "1")
	r := s.run(append([]string{"x", "p", "--"}, childPrinter("ENVER_KEY")...)...)
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if got := strings.TrimSpace(r.Stdout); got != cache.Key {
		t.Fatalf("the key-file key must be materialized as the child ENVER_KEY, got %q, want %q", got, cache.Key)
	}
	if strings.Contains(r.Stderr, "ENVER_FORWARD_KEY") {
		t.Fatalf("an explicit opt-in must not print the strip hint, stderr: %s", r.Stderr)
	}
}
