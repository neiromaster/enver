package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
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
	// broken extends ghost (undefined) → resolve error, must fall back to own count.
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
		{"broken", "0"},
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
