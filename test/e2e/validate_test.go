package e2e

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateNamedFiles pins the named-files mode: every file is reported in
// argument order under the path as given, publish-marked files are judged by
// the rules a pull applies, unmarked ones as local configs, a foreign file is
// skipped without counting, and the summary counts checked and failed files
// with the strict cause named only when a warning turned into a failure.
func TestValidateNamedFiles(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "team.yaml"), "publish: true\nprofiles:\n  \"llm/qwen\":\n    env:\n      K: v\n  work:\n    env:\n      ~API_TOKEN: sk-live\n")
	s.writeFile(filepath.Join(s.project, "ci.yaml"), "profiles:\n  \"llm/qwen\":\n    env:\n      K: v\n")
	s.writeFile(filepath.Join(s.project, "other.yaml"), "name: ci\non: [push]\n")
	s.writeFile(filepath.Join(s.project, "typo.yaml"), "publish: true\nprofile:\n  base: {}\n")
	s.writeFile(filepath.Join(s.project, "empty-marked.yaml"), "publish: true\n")
	files := []string{"team.yaml", "ci.yaml", "other.yaml", "typo.yaml", "empty-marked.yaml"}
	r := s.run(append([]string{"validate", "--strict"}, files...)...)
	if r.ExitCode != 1 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	for _, want := range []string{
		`team.yaml: error: profile "llm/qwen" is not a bare upstream profile name`,
		`team.yaml: warning: work: "API_TOKEN" is marked secret but stored in plaintext`,
		"ci.yaml: ✓ valid as a local config",
		"other.yaml: skipped: not an enver config",
		`typo.yaml: error: unknown top-level key "profile"`,
		"typo.yaml: warning: marked for publication but declares neither profiles nor remotes",
		"empty-marked.yaml: warning: marked for publication but declares neither profiles nor remotes",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Fatalf("stdout misses %q:\n%s", want, r.Stdout)
		}
	}
	if !strings.Contains(r.Stderr, "3 of 4 files failed (warnings treated as errors)") {
		t.Fatalf("summary: %s", r.Stderr)
	}
	first := []string{
		`team.yaml: error: profile "llm/qwen" is not a bare upstream profile name`,
		"ci.yaml: ✓ valid as a local config",
		"other.yaml: skipped: not an enver config",
		`typo.yaml: error: unknown top-level key "profile"`,
		"empty-marked.yaml: warning: marked for publication",
	}
	at := 0
	for _, line := range first {
		i := strings.Index(r.Stdout[at:], line)
		if i < 0 {
			t.Fatalf("%q does not come after the previous file's first line:\n%s", line, r.Stdout)
		}
		at += i
	}
	lax := s.run(append([]string{"validate"}, files...)...)
	if lax.ExitCode != 1 || !strings.Contains(lax.Stderr, "2 of 4 files failed") || strings.Contains(lax.Stderr, "treated as errors") {
		t.Fatalf("without --strict: exit %d, stderr: %s", lax.ExitCode, lax.Stderr)
	}
	if !strings.Contains(lax.Stdout, "empty-marked.yaml: warning: marked for publication") {
		t.Fatalf("the warning line must still print without --strict:\n%s", lax.Stdout)
	}
}

// TestValidateStrictMergedMode pins the no-files mode under --strict: a
// warning in the merged config fails the run and stderr names the strict
// cause, while the same config passes without the flag.
func TestValidateStrictMergedMode(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  e:\n")
	r := s.run("validate", "--strict")
	if r.ExitCode != 1 || !strings.Contains(r.Stderr, "warnings treated as errors") {
		t.Fatalf("exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if r := s.run("validate"); r.ExitCode != 0 {
		t.Fatalf("a warning alone must not fail without --strict: exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
}

// TestValidateFileNamesTheFileOnce pins the file label on both argument forms:
// a positional and a --file value are reported under the path itself, never
// re-labelled as a remote record, and the path is named exactly once across
// stdout and stderr.
func TestValidateFileNamesTheFileOnce(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "team.yaml"), "publish: true\nprofiles:\n  \"a/b\":\n    env:\n      K: v\n")
	for _, args := range [][]string{{"validate", "team.yaml"}, {"validate", "--file", "team.yaml"}} {
		r := s.run(args...)
		if r.ExitCode != 1 || strings.Contains(r.Stdout, `remote "team.yaml"`) {
			t.Fatalf("%v: exit %d, stdout: %s", args, r.ExitCode, r.Stdout)
		}
		if got := strings.Count(strings.TrimSpace(r.Stdout)+"\n"+strings.TrimSpace(r.Stderr), "team.yaml"); got != 1 {
			t.Fatalf("%v: the file is named %d times:\n%s%s", args, got, r.Stdout, r.Stderr)
		}
	}
}

// TestValidateEmptyFileIsSkipped pins the empty document: it declares nothing
// enver-shaped, so the file is skipped and the run still succeeds.
func TestValidateEmptyFileIsSkipped(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "empty.yaml"), "")
	r := s.run("validate", "empty.yaml")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "empty.yaml: skipped: not an enver config") {
		t.Fatalf("exit %d, stdout: %s, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}

// TestValidateDuplicateMark pins the duplicate-key refusal: a publish mark
// stated twice is an error in the file, while a clean file next to it is still
// reported valid and the run fails with exit 1.
func TestValidateDuplicateMark(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "dup.yaml"), "publish: true\npublish: true\nprofiles: {}\n")
	s.writeFile(filepath.Join(s.project, "ok.yaml"), "profiles:\n  p:\n    env:\n      K: v\n")
	r := s.run("validate", "dup.yaml", "ok.yaml")
	if r.ExitCode != 1 || !strings.Contains(r.Stdout, "dup.yaml: error:") || !strings.Contains(r.Stdout, "ok.yaml: ✓ valid as a local config") {
		t.Fatalf("exit %d, stdout: %s, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}

// TestValidateStrictWarnsOnEmptyProfile pins the severity ladder for one file:
// an empty profile is a warning that passes plain and fails under --strict
// with the strict cause on stderr.
func TestValidateStrictWarnsOnEmptyProfile(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "p.yaml"), "publish: true\nprofiles:\n  e:\n")
	if r := s.run("validate", "p.yaml"); r.ExitCode != 0 || !strings.Contains(r.Stdout, "p.yaml: warning: e: no env vars, no extends, and no unset") {
		t.Fatalf("exit %d, stdout: %s, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if r := s.run("validate", "--strict", "p.yaml"); r.ExitCode != 1 || !strings.Contains(r.Stderr, "warnings treated as errors") {
		t.Fatalf("exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
}

// TestValidatePublishRefusesUnusableRecord pins the record refusal: a marked
// file declaring a remote whose source cannot resolve is an error, so the file
// is never reported clean and the run fails.
func TestValidatePublishRefusesUnusableRecord(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "p.yaml"), "publish: true\nremotes:\n  r:\n    url: not a source\n")
	r := s.run("validate", "p.yaml")
	if r.ExitCode != 1 || !strings.Contains(r.Stdout, "p.yaml: error:") {
		t.Fatalf("exit %d, stdout: %s, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}

// TestValidateUnreadablePath pins the unreadable path: a named path nothing can
// be read from is checked, failed, and reported as a cannot-read error.
func TestValidateUnreadablePath(t *testing.T) {
	s := newSandbox(t)
	r := s.run("validate", s.project)
	if r.ExitCode != 1 || !strings.Contains(r.Stdout, "error: cannot read") {
		t.Fatalf("exit %d, stdout: %s, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}

// TestValidateNotePassesStrict pins the bottom rung of the severity ladder: a
// file whose only finding is a note passes even under --strict, and the note
// line takes the verdict line's place.
func TestValidateNotePassesStrict(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "p.yaml"),
		"remotes:\n  r:\n    url: https://example.invalid/r.yaml\n    no_prefix: true\nprofiles:\n  a:\n    extends: ghost\n")
	r := s.run("validate", "--strict", "p.yaml")
	if r.ExitCode != 0 ||
		!strings.Contains(r.Stdout, "p.yaml: note:") ||
		strings.Contains(r.Stdout, "✓ valid") {
		t.Fatalf("exit %d, stdout: %s, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}

// TestValidateUnreadablePathsDoNotStopTheRest pins the per-file error: paths
// nothing can be read from fail their own check while the healthy file beside
// them is still reported, and the summary counts both failures.
func TestValidateUnreadablePathsDoNotStopTheRest(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, "ok.yaml"), "profiles:\n  p:\n    env:\n      K: v\n")
	r := s.run("validate", s.project, "missing.yaml", "ok.yaml")
	if r.ExitCode != 1 ||
		!strings.Contains(r.Stdout, s.project+": error: cannot read:") ||
		!strings.Contains(r.Stdout, "missing.yaml: error: cannot read:") ||
		!strings.Contains(r.Stdout, "ok.yaml: ✓ valid as a local config") ||
		!strings.Contains(r.Stderr, "2 of 3 files failed") {
		t.Fatalf("exit %d, stdout: %s, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}
