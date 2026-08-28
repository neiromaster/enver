package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestImportWritesLocalLayer(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "incoming.env"), "TOKEN=abc\nMODE=fast\n")
	r := s.run("import", "incoming.env", "p")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "2 vars") {
		t.Fatalf("summary should count the imported vars, got: %q", r.Stdout)
	}
	local := s.readLocal()
	if !strings.Contains(local, "abc") || !strings.Contains(local, "fast") {
		t.Fatalf("imported values must land in the local layer, got: %q", local)
	}
}

func TestRemoveDropsProfile(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      A: \"1\"\n  q:\n    env:\n      A: \"2\"\n")
	r := s.run("remove", "-y", "p")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if got := s.readLocal(); strings.Contains(got, "p:") {
		t.Fatalf("removed profile must be gone from the file, got: %q", got)
	}
}

func TestRemoveRefusedWhenInherited(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  base:\n    env:\n      A: \"1\"\n  child:\n    extends: [base]\n")
	r := s.run("remove", "-y", "base")
	if r.ExitCode == 0 {
		t.Fatal("removing an extended profile must be refused")
	}
	if !strings.Contains(r.Stderr, "extended by") {
		t.Fatalf("refusal must name the dependents, got: %q", r.Stderr)
	}
	if got := s.readLocal(); !strings.Contains(got, "base:") {
		t.Fatalf("the refused profile must stay in the file, got: %q", got)
	}
}

func TestRenameMovesProfile(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      A: \"1\"\n")
	r := s.run("rename", "p", "q")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	local := s.readLocal()
	if strings.Contains(local, "p:") || !strings.Contains(local, "q:") {
		t.Fatalf("rename must move the profile in place, got: %q", local)
	}
}

func TestValidateAcceptsHealthyConfig(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      A: \"1\"\n")
	r := s.run("validate")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stdout: %s, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "config is valid") {
		t.Fatalf("validate should bless the config, got: %q", r.Stdout)
	}
}

func TestValidateReportsBrokenConfig(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles: [\n")
	r := s.run("validate")
	if r.ExitCode == 0 {
		t.Fatal("validate must fail on a broken config")
	}
	if !strings.Contains(r.Stderr, "parse") {
		t.Fatalf("validate should surface the parse error, got: %q", r.Stderr)
	}
}
