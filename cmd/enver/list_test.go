package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

func TestDoListVarsColumn(t *testing.T) {
	// base: 1 own var; also the default.
	path := writeTempConfig(t, "base", map[string]string{"X": "1"}, nil, true)
	// child extends base, 0 own vars → resolved total 1.
	if err := config.UpsertProfile(path, "child", config.Profile{Extends: config.Extends{"base"}}, false, false); err != nil {
		t.Fatalf("upsert child: %v", err)
	}
	// mix extends base, 2 own vars (A, B) → resolved total 3 (X + A + B).
	if err := config.UpsertProfile(path, "mix", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"A": "1", "B": "2"}}, false, false); err != nil {
		t.Fatalf("upsert mix: %v", err)
	}
	// broken extends ghost (undefined) → resolve error, cell must say so instead
	// of blending in with parentless profiles.
	if err := config.UpsertProfile(path, "broken", config.Profile{Extends: config.Extends{"ghost"}}, false, false); err != nil {
		t.Fatalf("upsert broken: %v", err)
	}
	// basedata extends base: its name contains the extends string, which tripped
	// the old substring-based cell helper.
	if err := config.UpsertProfile(path, "basedata", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"Y": "2"}}, false, false); err != nil {
		t.Fatalf("upsert basedata: %v", err)
	}
	withGlobalConfig(t, path)

	var out bytes.Buffer
	if err := doList(&out); err != nil {
		t.Fatalf("doList: %v", err)
	}

	header := strings.Split(out.String(), "\n")[0]
	varsCol := strings.Index(header, "VARS")
	if varsCol < 0 {
		t.Fatalf("missing VARS column in header:\n%s", out.String())
	}
	cases := []struct{ profile, want string }{
		{"base", "1"},
		{"child", "0 (→1)"},
		{"mix", "2 (→3)"},
		{"broken", "0 (→?)"},
		{"basedata", "1 (→2)"},
	}
	for _, c := range cases {
		line := findListLine(out.String(), c.profile)
		if line == "" {
			t.Fatalf("profile %q not found in output:\n%s", c.profile, out.String())
		}
		if got := strings.TrimRight(line[varsCol:], " "); got != c.want {
			t.Errorf("profile %q vars cell = %q, want %q (line: %q)", c.profile, got, c.want, line)
		}
	}

	if !strings.Contains(out.String(), "* = default") {
		t.Errorf("missing default footer:\n%s", out.String())
	}
}

// writeFencedConfig builds the unset-fencing fixture: base (one var, no unset)
// as the control, fenced (its only key stripped by its own unset list, no
// extends), and fencedext (same shape over a var-less parent so the extends
// branch resolves to an empty env).
func writeFencedConfig(t *testing.T) string {
	t.Helper()
	path := writeTempConfig(t, "base", map[string]string{"X": "1"}, nil, false)
	if err := config.UpsertProfile(path, "fenced", config.Profile{Env: map[string]string{"A": "1"}, Unset: config.Unsets{"A"}}, false, false); err != nil {
		t.Fatalf("upsert fenced: %v", err)
	}
	if err := config.UpsertProfile(path, "bare", config.Profile{}, false, false); err != nil {
		t.Fatalf("upsert bare: %v", err)
	}
	if err := config.UpsertProfile(path, "fencedext", config.Profile{Extends: config.Extends{"bare"}, Env: map[string]string{"A": "1"}, Unset: config.Unsets{"A"}}, false, false); err != nil {
		t.Fatalf("upsert fencedext: %v", err)
	}
	return path
}

// TestDoListVarsColumnExcludesUnsetFenced pins list against show: a key fenced
// by the profile's own unset list is absent from the resolved env, so both the
// own count and the resolved count must exclude it.
func TestDoListVarsColumnExcludesUnsetFenced(t *testing.T) {
	withGlobalConfig(t, writeFencedConfig(t))

	var out bytes.Buffer
	if err := doList(&out); err != nil {
		t.Fatalf("doList: %v", err)
	}

	header := strings.Split(out.String(), "\n")[0]
	varsCol := strings.Index(header, "VARS")
	if varsCol < 0 {
		t.Fatalf("missing VARS column in header:\n%s", out.String())
	}
	cases := []struct{ profile, want string }{
		{"base", "1"},
		{"fenced", "0"},
		{"fencedext", "0 (→0)"},
	}
	for _, c := range cases {
		line := findListLine(out.String(), c.profile)
		if line == "" {
			t.Fatalf("profile %q not found in output:\n%s", c.profile, out.String())
		}
		if got := strings.TrimRight(line[varsCol:], " "); got != c.want {
			t.Errorf("profile %q vars cell = %q, want %q (line: %q)", c.profile, got, c.want, line)
		}
	}
}

func TestDoListJSONUnsetFencedCounts(t *testing.T) {
	withGlobalConfig(t, writeFencedConfig(t))

	var out bytes.Buffer
	if err := doListJSON(&out); err != nil {
		t.Fatalf("doListJSON: %v", err)
	}
	var got listJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	byName := make(map[string]listJSONEntry, len(got.Profiles))
	for _, p := range got.Profiles {
		byName[p.Name] = p
	}
	for _, name := range []string{"fenced", "fencedext"} {
		if p := byName[name]; p.Vars != 0 || p.Resolved != 0 {
			t.Errorf("%s vars=%d resolved=%d, want 0/0", name, p.Vars, p.Resolved)
		}
	}
	if b := byName["base"]; b.Vars != 1 || b.Resolved != 1 {
		t.Errorf("base vars=%d resolved=%d, want 1/1 (control without unset)", b.Vars, b.Resolved)
	}
}

func TestDoListAlignsLongProfileNames(t *testing.T) {
	const long = "a-very-long-profile-name-thirty-chars"
	path := writeTempConfig(t, long, map[string]string{"X": "1"}, nil, true)
	if err := config.UpsertProfile(path, "base", config.Profile{Env: map[string]string{"Y": "2"}}, false, false); err != nil {
		t.Fatalf("upsert base: %v", err)
	}
	if err := config.UpsertProfile(path, "child", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"Z": "3"}}, false, false); err != nil {
		t.Fatalf("upsert child: %v", err)
	}
	withGlobalConfig(t, path)

	var out bytes.Buffer
	if err := doList(&out); err != nil {
		t.Fatalf("doList: %v", err)
	}

	lines := strings.Split(out.String(), "\n")
	header := lines[0]
	extCol := strings.Index(header, "EXTENDS")
	varsCol := strings.Index(header, "VARS")
	if extCol < 0 || varsCol < 0 {
		t.Fatalf("missing EXTENDS/VARS in header:\n%s", out.String())
	}

	rows := []struct {
		name, extends, vars string
	}{
		{long, "-", "1"},
		{"base", "-", "1"},
		{"child", "base", "1 (→2)"},
	}
	for _, r := range rows {
		line := findListLine(out.String(), r.name)
		if line == "" {
			t.Fatalf("row %q not found:\n%s", r.name, out.String())
		}
		if !strings.Contains(line, r.name) {
			t.Errorf("row %q truncated (line: %q)", r.name, line)
		}
		if got := line[extCol : extCol+len(r.extends)]; got != r.extends {
			t.Errorf("row %q EXTENDS at col %d = %q, want %q (line: %q)", r.name, extCol, got, r.extends, line)
		}
		if got := strings.TrimRight(line[varsCol:], " "); got != r.vars {
			t.Errorf("row %q VARS at col %d = %q, want %q (line: %q)", r.name, varsCol, got, r.vars, line)
		}
	}

	if !strings.Contains(out.String(), "* = default") {
		t.Errorf("missing default footer:\n%s", out.String())
	}
}

func TestDoListJSON(t *testing.T) {
	path := writeTempConfig(t, "base", map[string]string{"X": "1"}, nil, false)
	if err := config.UpsertProfile(path, "prod", config.Profile{Extends: config.Extends{"base"}, Env: map[string]string{"A": "1", "B": "2"}}, false, false); err != nil {
		t.Fatalf("upsert prod: %v", err)
	}
	if err := config.SetDefault(path, "prod"); err != nil {
		t.Fatalf("set default: %v", err)
	}
	withGlobalConfig(t, path)

	var out bytes.Buffer
	if err := doListJSON(&out); err != nil {
		t.Fatalf("doListJSON: %v", err)
	}
	var got listJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if len(got.Profiles) != 2 {
		t.Fatalf("got %d profiles, want 2:\n%s", len(got.Profiles), out.String())
	}
	byName := make(map[string]listJSONEntry, len(got.Profiles))
	for _, p := range got.Profiles {
		byName[p.Name] = p
	}
	prod := byName["prod"]
	if !prod.Default {
		t.Error("prod must be the default")
	}
	if !reflect.DeepEqual(prod.Extends, []string{"base"}) {
		t.Errorf("prod.extends = %v, want [base]", prod.Extends)
	}
	if prod.Vars != 2 || prod.Resolved != 3 {
		t.Errorf("prod vars=%d resolved=%d, want 2/3", prod.Vars, prod.Resolved)
	}
	if base := byName["base"]; base.Default || base.Vars != 1 || base.Resolved != 1 {
		t.Errorf("base default=%v vars=%d resolved=%d, want false/1/1", base.Default, base.Vars, base.Resolved)
	}
}

// TestDoListJSONBrokenResolve pins the machine-readable contract: a profile
// whose extends chain cannot resolve is an error, not a silently-degraded row.
// Text output may fall back to own-count for human display; JSON consumers need
// to know the profile is broken.
func TestDoListJSONBrokenResolve(t *testing.T) {
	path := writeTempConfig(t, "ok", map[string]string{"X": "1"}, nil, false)
	if err := config.UpsertProfile(path, "broken", config.Profile{Extends: config.Extends{"ghost"}}, false, false); err != nil {
		t.Fatalf("upsert broken: %v", err)
	}
	withGlobalConfig(t, path)

	var out bytes.Buffer
	if err := doListJSON(&out); err == nil {
		t.Fatalf("doListJSON must fail on an unresolvable extends chain, got:\n%s", out.String())
	}
}

// TestListJSONRemoteDanglingFails pins the machine-readable contract under
// strict scoping: a profile whose extends chain cannot resolve fails the
// listing, and a remote-authored gap is no exception. The only dangling
// reference a pulled file can declare names a record inside its own remotes:
// section, so that is the gap the listing refuses on.
func TestListJSONRemoteDanglingFails(t *testing.T) {
	t.Run("a declared-child gap fails the listing", func(t *testing.T) {
		f := newPullCmdFixture(t)
		writePullFile(t, f.globalPath,
			"remotes:\n  llm:\n    url: https://example.com/llm.yaml\n"+
				"profiles:\n  good:\n    env:\n      K: v\n")
		if err := config.WriteRemoteContent("llm", []byte("profiles:\n  qwen:\n    extends: claude/default\n    env:\n      MODEL: qwen\nremotes:\n  claude:\n    url: https://example.com/claude.yaml\n")); err != nil {
			t.Fatal(err)
		}
		if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/llm.yaml"}); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		err := runListJSON(t, &out)
		if err == nil || !strings.Contains(err.Error(), `profile "llm/claude/default" not found`) {
			t.Fatalf("a dangling child target must fail the JSON listing, got: %v\n%s", err, out.String())
		}
	})

	t.Run("a cycle among remote profiles still fails", func(t *testing.T) {
		f := newPullCmdFixture(t)
		writePullFile(t, f.globalPath,
			"remotes:\n  llm:\n    url: https://example.com/llm.yaml\n"+
				"profiles:\n  good:\n    env:\n      K: v\n")
		if err := config.WriteRemoteContent("llm", []byte("profiles:\n  a:\n    extends: b\n  b:\n    extends: a\n")); err != nil {
			t.Fatal(err)
		}
		if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/llm.yaml"}); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer
		err := runListJSON(t, &out)
		if !errors.Is(err, config.ErrExtendsCycle) {
			t.Fatalf("a cycle is not a remote gap and must fail the listing, got: %v\n%s", err, out.String())
		}
	})
}

// runListJSON invokes the JSON listing the way a test can: RunE directly, with
// the format flag set as a caller would set it.
func runListJSON(t *testing.T, out io.Writer) error {
	t.Helper()
	prev := listFormat
	listFormat = "json"
	t.Cleanup(func() { listFormat = prev })
	cmd := &cobra.Command{}
	cmd.SetOut(out)
	return listCmd.RunE(cmd, nil)
}

func findListLine(out, profile string) string {
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) == 0 {
			continue
		}
		i := 0
		if f[0] == "*" {
			i = 1
		}
		if i < len(f) && f[i] == profile {
			return l
		}
	}
	return ""
}

// TestDoListVarsCountClosestWins: base defines A, mid unsets it, leaf
// redefines it — the closest mention wins, so leaf's definition survives and
// the own count claims it.
func TestDoListVarsCountClosestWins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	mustUpsert := func(name string, p config.Profile) {
		t.Helper()
		if err := config.UpsertProfile(path, name, p, false, false); err != nil {
			t.Fatalf("upsert %s: %v", name, err)
		}
	}
	mustUpsert("base", config.Profile{Env: map[string]string{"A": "0"}})
	mustUpsert("mid", config.Profile{Extends: config.Extends{"base"}, Unset: []string{"A"}})
	mustUpsert("leaf", config.Profile{Extends: config.Extends{"mid"}, Env: map[string]string{"A": "1"}})
	withGlobalConfig(t, path)

	var out bytes.Buffer
	if err := doList(&out); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out.String(), "\n") {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == "leaf" && (f[len(f)-2] != "1" || f[len(f)-1] != "(→1)") {
			t.Errorf("leaf row = %q, own count must claim the closest-wins redefinition (1 (→1))", line)
		}
	}

	var jout bytes.Buffer
	if err := doListJSON(&jout); err != nil {
		t.Fatal(err)
	}
	var got listJSON
	if err := json.Unmarshal(jout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Profiles {
		if p.Name == "leaf" && (p.Vars != 1 || p.Resolved != 1) {
			t.Errorf("leaf vars=%d resolved=%d, want 1/1", p.Vars, p.Resolved)
		}
	}
}
