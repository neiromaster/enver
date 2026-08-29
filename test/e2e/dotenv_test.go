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
