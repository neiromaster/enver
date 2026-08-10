package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

func TestDoListVarsColumn(t *testing.T) {
	// base: 1 own var; also the default.
	path := writeTempConfig(t, "base", map[string]string{"X": "1"}, nil, true)
	// child extends base, 0 own vars → resolved total 1.
	if err := config.UpsertProfile(path, "child", config.Profile{Extends: "base"}, false, false, nil); err != nil {
		t.Fatalf("upsert child: %v", err)
	}
	// mix extends base, 2 own vars (A, B) → resolved total 3 (X + A + B).
	if err := config.UpsertProfile(path, "mix", config.Profile{Extends: "base", Env: map[string]string{"A": "1", "B": "2"}}, false, false, nil); err != nil {
		t.Fatalf("upsert mix: %v", err)
	}
	// broken extends ghost (undefined) → resolve error, must fall back to own count.
	if err := config.UpsertProfile(path, "broken", config.Profile{Extends: "ghost"}, false, false, nil); err != nil {
		t.Fatalf("upsert broken: %v", err)
	}
	withGlobalConfig(t, path)

	var out bytes.Buffer
	if err := doList(&out); err != nil {
		t.Fatalf("doList: %v", err)
	}

	cases := []struct{ profile, extends, want string }{
		{"base", "-", "1"},
		{"child", "base", "0 (→1)"},
		{"mix", "base", "2 (→3)"},
		{"broken", "ghost", "0"},
	}
	for _, c := range cases {
		line := findListLine(out.String(), c.profile)
		if line == "" {
			t.Fatalf("profile %q not found in output:\n%s", c.profile, out.String())
		}
		if got := varsCellAfter(line, c.extends); got != c.want {
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
	if err := config.UpsertProfile(path, "base", config.Profile{Env: map[string]string{"Y": "2"}}, false, false, nil); err != nil {
		t.Fatalf("upsert base: %v", err)
	}
	if err := config.UpsertProfile(path, "child", config.Profile{Extends: "base", Env: map[string]string{"Z": "3"}}, false, false, nil); err != nil {
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

// varsCellAfter returns the trimmed text following the extends display value,
// i.e. the VARS cell at the end of a list data row.
func varsCellAfter(line, extends string) string {
	idx := strings.Index(line, extends)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(line[idx+len(extends):])
}
