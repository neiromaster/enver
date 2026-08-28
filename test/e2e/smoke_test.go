package e2e

import (
	"strings"
	"testing"
)

func TestHelpExitsZero(t *testing.T) {
	s := newSandbox(t)
	r := s.run("--help")
	if r.ExitCode != 0 {
		t.Fatalf("enver --help = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "Usage:") {
		t.Fatalf("--help output lacks usage: %q", r.Stdout)
	}
}

func TestUnknownCommandFailsWithMessage(t *testing.T) {
	s := newSandbox(t)
	r := s.run("definitely-not-a-command")
	if r.ExitCode == 0 {
		t.Fatal("unknown command must exit non-zero")
	}
	if !strings.Contains(r.Stderr, "enver:") {
		t.Fatalf("unknown command stderr lacks the enver prefix: %q", r.Stderr)
	}
}
