package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDotenvRendersUnsetRows pins the tombstone contract at the binary
// boundary: the exported document comments out unset keys in the same sort as
// the live keys, naming the unsetting profile, and the -o file carries the
// same bytes as stdout.
func TestDotenvRendersUnsetRows(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n" +
		"  base:\n    env:\n      API_URL: https://api.example.com\n      DEBUG: \"0\"\n" +
		"  prod:\n    extends: [base]\n    unset: [DEBUG]\n")
	r := s.run("dotenv", "prod")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "# DEBUG=  # unset by \"prod\"") {
		t.Fatalf("the document must comment out the unset key with attribution, got: %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "API_URL=https://api.example.com") {
		t.Fatalf("live inherited vars must survive, got: %q", r.Stdout)
	}
	if ia, ib := strings.Index(r.Stdout, "API_URL="), strings.Index(r.Stdout, "# DEBUG="); ia < 0 || ib < 0 || ia > ib {
		t.Fatalf("unset rows must interleave in the same sort, got: %q", r.Stdout)
	}

	out := filepath.Join(s.project, "prod.env")
	r = s.run("dotenv", "prod", "-o", "prod.env")
	if r.ExitCode != 0 {
		t.Fatalf("-o exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "# DEBUG=  # unset by \"prod\"") {
		t.Fatalf("the written file must carry the tombstone row, got: %q", string(b))
	}
}

// TestDotenvImportRoundTripDropsTombstones pins the round-trip contract end
// to end: a rendered document re-imports without its tombstone rows — they
// are renderer metadata re-derived from the target chain — so import can
// neither plant a fake tombstone on a profile that never unset anything nor
// grow the unsetting profile's document on every cycle.
func TestDotenvImportRoundTripDropsTombstones(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n" +
		"  root:\n    env:\n      A: \"1\"\n      B: \"2\"\n" +
		"  mid:\n    extends: [root]\n    unset: [A]\n" +
		"  leaf:\n    extends: [mid]\n")

	r := s.run("dotenv", "leaf", "-o", "leaf.env")
	if r.ExitCode != 0 {
		t.Fatalf("dotenv -o exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "(1 vars, 1 unset)") {
		t.Fatalf("confirmation must count the unset row, got: %q", r.Stdout)
	}

	r = s.run("import", "leaf.env", "staging")
	if r.ExitCode != 0 {
		t.Fatalf("import exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	r = s.run("dotenv", "staging")
	if r.ExitCode != 0 {
		t.Fatalf("staging dotenv exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if strings.Contains(r.Stdout, "unset by") {
		t.Fatalf("import planted a tombstone on a profile that never unset anything:\n%s", r.Stdout)
	}
	if strings.Contains(s.readLocal(), "A=") {
		t.Fatalf("the tombstone row landed in the config as a comment:\n%s", s.readLocal())
	}

	for _, step := range []struct {
		args []string
		when string
	}{
		{[]string{"import", "leaf.env", "leaf"}, "re-import"},
		{[]string{"dotenv", "leaf"}, "second render"},
		{[]string{"import", "leaf.env", "leaf"}, "second re-import"},
		{[]string{"dotenv", "leaf"}, "third render"},
	} {
		if r := s.run(step.args...); r.ExitCode != 0 {
			t.Fatalf("%s (%v) exit = %d, stderr: %s", step.when, step.args, r.ExitCode, r.Stderr)
		}
	}
	first := s.run("dotenv", "leaf")
	third := s.run("dotenv", "leaf")
	if first.Stdout != third.Stdout {
		t.Fatalf("re-import changed the document:\nfirst:\n%s\nlast:\n%s", first.Stdout, third.Stdout)
	}
	if n := strings.Count(third.Stdout, "A="); n != 1 {
		t.Fatalf("exactly one A tombstone row expected, got %d:\n%s", n, third.Stdout)
	}
}
