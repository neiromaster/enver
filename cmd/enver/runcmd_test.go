package main

import (
	"slices"
	"strings"
	"testing"
)

func TestParseProfileAndCmd(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		dashAt   int
		wantProf string
		wantCmd  []string
	}{
		{"profile then command", []string{"anth", "claude"}, 1, "anth", []string{"claude"}},
		{"default profile via dash", []string{"claude"}, 0, "", []string{"claude"}},
		{"command keeps its own flags", []string{"anth", "claude", "--model", "x"}, 1, "anth", []string{"claude", "--model", "x"}},
		{"no dash: profile only", []string{"anth"}, -1, "anth", nil},
		{"no dash: empty", nil, -1, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prof, cmd := parseProfileAndCmd(c.args, c.dashAt)
			if prof != c.wantProf {
				t.Errorf("profile = %q, want %q", prof, c.wantProf)
			}
			if !slices.Equal(cmd, c.wantCmd) {
				t.Errorf("cmdArgs = %v, want %v", cmd, c.wantCmd)
			}
		})
	}
}

func TestDoRunRequiresCommand(t *testing.T) {
	// No command after `--` → must error before touching config or exec.
	cases := []struct {
		name   string
		args   []string
		dashAt int
	}{
		{"profile but no command", []string{"anth"}, -1},
		{"nothing at all", nil, -1},
		{"dash but nothing after", nil, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := doRun(c.args, c.dashAt)
			if err == nil {
				t.Fatal("doRun returned nil, want an error")
			}
			if !strings.Contains(err.Error(), "requires a command") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
