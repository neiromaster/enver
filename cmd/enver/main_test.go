package main

import "testing"

func TestParseArgsProfileAndCommand(t *testing.T) {
	o, err := parseArgs([]string{"anth", "--", "claude", "--model", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if o.profile != "anth" {
		t.Fatalf("profile = %q, want anth", o.profile)
	}
	if len(o.cmdArgs) != 3 || o.cmdArgs[0] != "claude" || o.cmdArgs[2] != "x" {
		t.Fatalf("cmdArgs = %v", o.cmdArgs)
	}
}

func TestParseArgsDefaultProfile(t *testing.T) {
	o, err := parseArgs([]string{"--", "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if o.profile != "" {
		t.Fatalf("profile = %q, want empty (default)", o.profile)
	}
	if len(o.cmdArgs) != 1 || o.cmdArgs[0] != "claude" {
		t.Fatalf("cmdArgs = %v", o.cmdArgs)
	}
}

func TestParseArgsFlagsBeforeCommand(t *testing.T) {
	o, err := parseArgs([]string{"--no-local", "anth", "--print"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.noLocal || !o.printMode || o.profile != "anth" {
		t.Fatalf("flags not set: %+v", o)
	}
}

func TestParseArgsKeyWithValue(t *testing.T) {
	cases := []struct {
		args    []string
		wantKey string
		wantCfg string
	}{
		{[]string{"--key", "/p/k", "anth"}, "/p/k", ""},
		{[]string{"anth", "--key=/p/k"}, "/p/k", ""},
		{[]string{"--config", "/c", "anth"}, "", "/c"},
		{[]string{"anth", "--config=/c"}, "", "/c"},
		{[]string{"--config", "/c", "--key", "/k", "anth"}, "/k", "/c"},
	}
	for _, c := range cases {
		o, err := parseArgs(c.args)
		if err != nil {
			t.Fatalf("parse %v: %v", c.args, err)
		}
		if o.keyPath != c.wantKey {
			t.Fatalf("keyPath for %v = %q, want %q", c.args, o.keyPath, c.wantKey)
		}
		if o.configPath != c.wantCfg {
			t.Fatalf("configPath for %v = %q, want %q", c.args, o.configPath, c.wantCfg)
		}
		if o.profile != "anth" {
			t.Fatalf("profile for %v = %q, want anth", c.args, o.profile)
		}
	}
}

func TestParseArgsKeyRequiresValue(t *testing.T) {
	if _, err := parseArgs([]string{"--key"}); err == nil {
		t.Fatal("--key without value should error")
	}
	if _, err := parseArgs([]string{"--config"}); err == nil {
		t.Fatal("--config without value should error")
	}
}

func TestParseArgsUnknownFlag(t *testing.T) {
	if _, err := parseArgs([]string{"--bogus"}); err == nil {
		t.Fatal("unknown flag should error")
	}
}

func TestParseArgsTwoProfiles(t *testing.T) {
	if _, err := parseArgs([]string{"anth", "glm"}); err == nil {
		t.Fatal("two positional profile args should error")
	}
}

func TestParseArgsDashPassthrough(t *testing.T) {
	// a bare "-" is not a flag; treated as positional profile
	o, err := parseArgs([]string{"-"})
	if err != nil {
		t.Fatal(err)
	}
	if o.profile != "-" {
		t.Fatalf("profile = %q, want -", o.profile)
	}
}

func TestHasSeparator(t *testing.T) {
	if !hasSeparator([]string{"anth", "--", "claude"}) {
		t.Fatal("expected separator")
	}
	if hasSeparator([]string{"anth"}) {
		t.Fatal("no separator expected")
	}
}

func TestIsCryptSubcommand(t *testing.T) {
	for _, s := range []string{"keygen", "encrypt", "decrypt"} {
		if !isCryptSubcommand(s) {
			t.Fatalf("%q should be a crypt subcommand", s)
		}
	}
	for _, s := range []string{"init", "anth", ""} {
		if isCryptSubcommand(s) {
			t.Fatalf("%q should not be a crypt subcommand", s)
		}
	}
}