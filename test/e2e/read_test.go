package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShowLocalProfile(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      MODE: fast\n")
	r := s.run("show", "p")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "MODE=fast") {
		t.Fatalf("show output lacks the var: %q", r.Stdout)
	}
}

func TestShowLayersLocalWinsOverGlobal(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("profiles:\n  p:\n    env:\n      A: g\n")
	s.writeLocal("profiles:\n  p:\n    env:\n      A: l\n")
	r := s.run("show", "p")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "A=l") || strings.Contains(r.Stdout, "A=g") {
		t.Fatalf("local must win the merge, got: %q", r.Stdout)
	}
}

func TestShowNoLocalReadsGlobalOnly(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("profiles:\n  p:\n    env:\n      A: g\n")
	s.writeLocal("profiles:\n  p:\n    env:\n      A: l\n")
	r := s.run("show", "--no-local", "p")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "A=g") || strings.Contains(r.Stdout, "A=l") {
		t.Fatalf("--no-local must drop the local layer, got: %q", r.Stdout)
	}
}

func TestDefaultSetShowAndListMarker(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      A: \"1\"\n  q:\n    env:\n      A: \"2\"\n")
	if r := s.run("default", "p"); r.ExitCode != 0 {
		t.Fatalf("default set: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if got := s.readLocal(); !strings.Contains(got, "default: p") {
		t.Fatalf("default must be written to the local layer, got: %q", got)
	}
	if r := s.run("default"); r.ExitCode != 0 || !strings.Contains(r.Stdout, "p") {
		t.Fatalf("default show = %d, %q", r.ExitCode, r.Stdout)
	}
	r := s.run("list")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "*") || !strings.Contains(r.Stdout, "q") {
		t.Fatalf("list = %d, %q; want the * marker on the default and q listed", r.ExitCode, r.Stdout)
	}
	r = s.run("show")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "A=1") {
		t.Fatalf("bare show must resolve the default profile, got %d, %q", r.ExitCode, r.Stdout)
	}
}

func TestDefaultGlobalFlagWritesGlobalLayer(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("profiles:\n  p:\n    env:\n      A: \"1\"\n")
	r := s.run("default", "p", "--global")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if _, err := os.Stat(s.localPath()); !os.IsNotExist(err) {
		t.Fatalf("--global must not create the local layer, stat err: %v", err)
	}
	if got := s.readFile(s.configPath()); !strings.Contains(got, "default: p") {
		t.Fatalf("global layer must hold the default, got: %q", got)
	}
}

func TestChdirReadsLocalFromGivenDir(t *testing.T) {
	s := newSandbox(t)
	other := filepath.Join(s.home, "other")
	s.writeFile(filepath.Join(other, ".enver.yaml"), "profiles:\n  p:\n    env:\n      A: other\n")
	r := s.run("--chdir", other, "show", "p")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "A=other") {
		t.Fatalf("--chdir show = %d, %q, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}

func TestShowJSONUnmasked(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      TOKEN: secretvalue1\n")
	var payload struct {
		Profile string            `json:"profile"`
		Env     map[string]string `json:"env"`
	}
	r := s.run("show", "--format", "json", "p")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		t.Fatalf("show json is not valid JSON: %v\n%s", err, r.Stdout)
	}
	if payload.Env["TOKEN"] != "secretvalue1" {
		t.Fatalf("json output must be unmasked, got: %v", payload.Env)
	}
	text := s.run("show", "p")
	if !strings.Contains(text.Stdout, "secr…(len=12)") || strings.Contains(text.Stdout, "secretvalue1") {
		t.Fatalf("text output must mask the secret, got: %q", text.Stdout)
	}
}

func TestListJSONUnmasked(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      TOKEN: secretvalue1\n  q:\n    env:\n      A: \"1\"\n")
	if r := s.run("default", "p"); r.ExitCode != 0 {
		t.Fatalf("default set: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	r := s.run("list", "--format", "json")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	type entry struct {
		Name     string `json:"name"`
		Default  bool   `json:"default"`
		Vars     int    `json:"vars"`
		Resolved int    `json:"resolved"`
	}
	var payload struct {
		Profiles []entry `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &payload); err != nil {
		t.Fatalf("list json is not valid JSON: %v\n%s", err, r.Stdout)
	}
	var p, q *entry
	for i := range payload.Profiles {
		switch e := &payload.Profiles[i]; e.Name {
		case "p":
			p = e
		case "q":
			q = e
		}
	}
	if p == nil || q == nil {
		t.Fatalf("list json must name both profiles, got: %+v", payload.Profiles)
	}
	if !p.Default || q.Default {
		t.Fatalf("the default marker must identify p only, got: %+v", payload.Profiles)
	}
	if p.Vars != 1 || p.Resolved != 1 || q.Vars != 1 || q.Resolved != 1 {
		t.Fatalf("list json must carry the per-profile var counts, got: %+v", payload.Profiles)
	}
	// list json carries counts, never values, so the unmasked contract means
	// no masked placeholder can appear in the payload at all.
	if strings.Contains(r.Stdout, "…(len=") {
		t.Fatalf("list json must carry no masked placeholder, got: %q", r.Stdout)
	}
}

func TestShowHidesFencedVar(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    unset: [SECRET]\n    env:\n      SECRET: s\n      A: \"1\"\n")
	r := s.run("show", "p")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if strings.Contains(r.Stdout, "SECRET") {
		t.Fatalf("a fenced var must not reach the resolved env, got: %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "A=1") {
		t.Fatalf("unfenced vars survive, got: %q", r.Stdout)
	}
}
