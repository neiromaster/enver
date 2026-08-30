package e2e

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAddWithoutTTYFails(t *testing.T) {
	s := newSandbox(t)
	r := s.run("add")
	if r.ExitCode == 0 {
		t.Fatal("add must refuse without a terminal")
	}
	if !strings.Contains(r.Stderr, "add is interactive") {
		t.Fatalf("refusal should name the command, got: %q", r.Stderr)
	}
}

func TestEditWithoutTTYFails(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      A: \"1\"\n")
	r := s.run("edit", "p")
	if r.ExitCode == 0 {
		t.Fatal("edit must refuse without a terminal")
	}
	if !strings.Contains(r.Stderr, "edit is interactive") {
		t.Fatalf("refusal should name the command, got: %q", r.Stderr)
	}
}

func TestImportWithoutProfileArgFailsWithoutTTY(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "incoming.env"), "TOKEN=abc\n")
	r := s.run("import", "incoming.env")
	if r.ExitCode == 0 {
		t.Fatal("import without a profile arg must refuse without a terminal")
	}
	if !strings.Contains(r.Stderr, "pass it as an argument") {
		t.Fatalf("refusal should explain the fix, got: %q", r.Stderr)
	}
}

func TestCompleteListsProfiles(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      A: \"1\"\n  q:\n    env:\n      A: \"2\"\n")
	r := s.run("__complete", "show", "")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	var tokens []string
	for _, line := range strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n") {
		token, _, _ := strings.Cut(line, "\t")
		if token == "" || strings.HasPrefix(token, ":") {
			continue // the ":<directive>" tail line is cobra plumbing, not a candidate
		}
		tokens = append(tokens, token)
	}
	if !slices.Contains(tokens, "p") || !slices.Contains(tokens, "q") {
		t.Fatalf("completion must offer the profile tokens, got: %q", r.Stdout)
	}
}

// TestShowPipedOutputStaysEscapeFree pins the documented pipe contract end to
// end: text show through a pipe never emits ANSI escapes, even in a
// color-capable environment with NO_COLOR absent.
func TestShowPipedOutputStaysEscapeFree(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      A: \"1\"\n")
	s.setEnv("NO_COLOR", "")
	s.setEnv("TERM", "xterm-256color")
	r := s.run("show", "p")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if strings.ContainsRune(r.Stdout, '\x1b') {
		t.Fatalf("piped show output carries ANSI escapes: %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "A=1") {
		t.Fatalf("piped show lost the value: %q", r.Stdout)
	}
}
