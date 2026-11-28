package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

func TestDedupIssuesSortedByFileProfileKind(t *testing.T) {
	out := dedupIssues([]config.Issue{
		{File: "b.yaml", Profile: "a", Kind: config.KindCycle},
		{File: "c.yaml", Profile: "b", Kind: config.KindDanglingExtends},
		{File: "a.yaml", Profile: "a", Kind: config.KindDanglingExtends},
		{File: "a.yaml", Profile: "b", Kind: config.KindCycle},
	})
	// Current sort order: by File, then Profile, then Kind
	// "cycle" < "dangling-extends" alphabetically
	want := []struct {
		file, profile string
		kind          config.IssueKind
	}{
		{"a.yaml", "a", config.KindDanglingExtends},
		{"a.yaml", "b", config.KindCycle},
		{"b.yaml", "a", config.KindCycle},
		{"c.yaml", "b", config.KindDanglingExtends},
	}
	for i, w := range want {
		if out[i].File != w.file || out[i].Profile != w.profile || out[i].Kind != w.kind {
			t.Fatalf("out[%d] = %s/%s/%s, want %s/%s/%s", i, out[i].File, out[i].Profile, out[i].Kind, w.file, w.profile, w.kind)
		}
	}
}

func TestCheckFile(t *testing.T) {
	for _, c := range []struct {
		name, path, in string
		strict         bool
		want           []string
		failed         bool
		exact          int
	}{
		{"publish slash", "team.yaml", "publish: true\nprofiles:\n  \"llm/qwen\":\n    env:\n      K: v\n", true,
			[]string{`team.yaml: error: profile "llm/qwen" is not a bare upstream profile name`}, true, 0},
		{"local slash", "ci.yaml", "profiles:\n  \"llm/qwen\":\n    env:\n      K: v\n", true,
			[]string{"ci.yaml: ✓ valid as a local config"}, false, 0},
		{"skipped", "other.yaml", "name: ci\non: [push]\n", true,
			[]string{"other.yaml: skipped: not an enver config"}, false, 0},
		{"unknown key", "typo.yaml", "publish: true\nprofiles: {}\nprofile: {}\n", true,
			[]string{`typo.yaml: error: unknown top-level key "profile"`}, true, 0},
		{"marked empty, strict", "m.yaml", "publish: true\n", true,
			[]string{"m.yaml: warning: marked for publication but declares neither profiles nor remotes"}, true, 0},
		{"marked empty, lax", "m.yaml", "publish: true\n", false,
			[]string{"m.yaml: warning: marked for publication but declares neither profiles nor remotes"}, false, 0},
		{"bad mark", "b.yaml", "publish: yes\nprofiles: {}\n", true,
			[]string{"b.yaml: error: publish: want the boolean true"}, true, 0},
		{"misplaced mark", "mp.yaml", "profiles:\n  a:\n    publish: true\n    env:\n      K: v\n", true,
			[]string{"mp.yaml: error: publish is not a profile field; the mark belongs at the top level of the file"}, true, 0},
		{"type error", "t.yaml", "publish: true\nprofiles: []\n", true,
			[]string{"t.yaml: error:", "cannot unmarshal"}, true, 1},
		{"env value type error", "e.yaml", "profiles:\n  a:\n    env:\n      K: [x]\n", true,
			[]string{"e.yaml: error:", "cannot unmarshal"}, true, 1},
	} {
		lines, failed := checkFile(c.path, []byte(c.in), c.strict)
		if failed != c.failed {
			t.Errorf("%s: failed = %v, want %v", c.name, failed, c.failed)
		}
		if c.exact > 0 && len(lines) != c.exact {
			t.Errorf("%s: %d lines, want exactly %d: %q", c.name, len(lines), c.exact, lines)
		}
		for _, l := range lines {
			if strings.Contains(l, "\n") {
				t.Errorf("%s: line %q carries a newline; every finding is one line", c.name, l)
			}
		}
		for _, w := range c.want {
			if !slices.ContainsFunc(lines, func(l string) bool { return strings.Contains(l, w) }) {
				t.Errorf("%s: lines %q lack %q", c.name, lines, w)
			}
		}
	}
}

func TestRunFileValidateSummary(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good := write("good.yaml", "profiles:\n  p:\n    env:\n      K: v\n")
	skipped := write("note.yaml", "name: ci\non: [push]\n")
	warn := write("warn.yaml", "publish: true\n")
	bad := write("bad.yaml", "publish: true\nprofiles:\n  \"a/b\":\n    env:\n      K: v\n")
	missing := filepath.Join(dir, "missing.yaml")
	all := []string{good, skipped, warn, bad, missing}

	var out bytes.Buffer
	err := runFileValidate(&out, all, false)
	if err == nil || err.Error() != "2 of 4 files failed" || strings.Contains(err.Error(), "treated as errors") {
		t.Fatalf("lax summary = %v", err)
	}
	for _, want := range []string{
		"good.yaml: ✓ valid as a local config",
		"note.yaml: skipped: not an enver config",
		"warn.yaml: warning: marked for publication but declares neither profiles nor remotes",
		`bad.yaml: error: profile "a/b" is not a bare upstream profile name`,
		"missing.yaml: error: cannot read:",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout lacks %q:\n%s", want, out.String())
		}
	}
	if i, j := strings.Index(out.String(), "good.yaml:"), strings.Index(out.String(), "missing.yaml:"); i > j {
		t.Fatalf("files out of order:\n%s", out.String())
	}

	out.Reset()
	if err := runFileValidate(&out, all, true); err == nil || err.Error() != "3 of 4 files failed (warnings treated as errors)" {
		t.Fatalf("strict summary = %v", err)
	}

	out.Reset()
	if err := runFileValidate(&out, []string{bad, missing}, true); err == nil || err.Error() != "2 of 2 files failed" || strings.Contains(err.Error(), "treated as errors") {
		t.Fatalf("errors-only strict summary = %v", err)
	}

	out.Reset()
	if err := runFileValidate(&out, []string{good, skipped}, false); err != nil {
		t.Fatalf("a run without a failing file = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "skipped: not an enver config") || !strings.Contains(got, "✓ valid as a local config") || strings.Contains(got, "failed") {
		t.Fatalf("clean run stdout:\n%s", got)
	}
}

func TestValidateMergedStrictFailsOnWarning(t *testing.T) {
	chdirTemp(t)
	saveGlobalFlags(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := os.WriteFile(config.LocalPath(), []byte("profiles:\n  e:\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	validateStrict = true
	t.Cleanup(func() { validateStrict = false })

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := validateCmd.RunE(cmd, nil); err == nil || err.Error() != "config has warnings treated as errors (--strict)" {
		t.Fatalf("strict over a warning = %v", err)
	}
	if !strings.Contains(out.String(), "warning: e: no env vars, no extends, and no unset") {
		t.Fatalf("stdout lacks the warning line:\n%s", out.String())
	}

	validateStrict = false
	out.Reset()
	if err := validateCmd.RunE(cmd, nil); err != nil {
		t.Fatalf("a warning alone must not fail without --strict: %v", err)
	}
}

func TestValidateFilesUnderFileFlag(t *testing.T) {
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	for _, name := range []string{"a.yaml", "b.yaml"} {
		body := "publish: true\nprofiles:\n  \"x/y\":\n    env:\n      K: v\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	globalFlags.file = "a.yaml"

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := validateCmd.RunE(cmd, []string{"b.yaml"}); err == nil || err.Error() != "2 of 2 files failed" {
		t.Fatalf("RunE = %v", err)
	}
	if i, j := strings.Index(out.String(), "a.yaml:"), strings.Index(out.String(), "b.yaml:"); i > j {
		t.Fatalf("--file's file must be checked first:\n%s", out.String())
	}
}

// TestCheckFileVerdictDanglingDefault pins the local-file default check: a
// default naming no profile is an error, so the file fails without --strict,
// and the line carries no profile prefix to misread as one.
func TestCheckFileVerdictDanglingDefault(t *testing.T) {
	v := checkFileVerdict("ci.yaml", []byte("profiles:\n  dev:\n    env:\n      K: v\ndefault: ghost\n"), false)
	if !v.failed {
		t.Fatalf("failed = false without --strict, lines: %q", v.lines)
	}
	if want := `ci.yaml: error: default "ghost" names no profile`; !slices.Contains(v.lines, want) {
		t.Fatalf("lines = %q, want %q", v.lines, want)
	}
}

// TestCheckFileVerdictPublishIgnoresDefault pins the publish file's exemption:
// a marked file is judged by what a consumer's pull applies, so the local-file
// default check stays out of it.
func TestCheckFileVerdictPublishIgnoresDefault(t *testing.T) {
	v := checkFileVerdict("team.yaml", []byte("publish: true\nprofiles:\n  dev:\n    env:\n      K: v\ndefault: ghost\n"), false)
	if v.failed {
		t.Fatalf("a publish file must not fail on its default, lines: %q", v.lines)
	}
	for _, line := range v.lines {
		if strings.Contains(line, "names no profile") {
			t.Fatalf("publish file got the local-file default check: %q", v.lines)
		}
	}
}
