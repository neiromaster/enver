package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMerge(t *testing.T) {
	base := Config{
		Default: "a",
		Profiles: map[string]Profile{
			"a": {Env: map[string]string{"K1": "base", "K2": "base"}},
			"b": {Env: map[string]string{"K3": "base"}},
		},
	}
	over := Config{
		Default: "c",
		Profiles: map[string]Profile{
			"a": {Env: map[string]string{"K2": "over", "K4": "over"}},
			"c": {Extends: "a", Env: map[string]string{"K5": "over"}},
		},
	}
	got := Merge(base, over)
	if got.Default != "c" {
		t.Fatalf("default = %q, want c", got.Default)
	}
	a := got.Profiles["a"]
	if a.Env["K1"] != "base" || a.Env["K2"] != "over" || a.Env["K4"] != "over" {
		t.Fatalf("merge of a wrong: %v", a.Env)
	}
	if _, ok := got.Profiles["b"]; !ok {
		t.Fatal("profile b dropped by merge")
	}
	if got.Profiles["c"].Extends != "a" {
		t.Fatalf("c.extends = %q, want a", got.Profiles["c"].Extends)
	}
}

func TestResolveProfileExtends(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"root": {Env: map[string]string{"A": "1", "B": "1"}},
		"mid":  {Extends: "root", Env: map[string]string{"B": "2", "C": "2"}},
		"leaf": {Extends: "mid", Env: map[string]string{"C": "3"}},
	}}
	env, chain, err := cfg.ResolveProfile("leaf")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := chain, []string{"leaf", "mid", "root"}; !sliceEq(got, want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
	want := map[string]string{"A": "1", "B": "2", "C": "3"}
	if !mapEq(env, want) {
		t.Fatalf("env = %v, want %v", env, want)
	}
}

func TestResolveProfileCycle(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"a": {Extends: "b"},
		"b": {Extends: "a"},
	}}
	_, _, err := cfg.ResolveProfile("a")
	if err == nil {
		t.Fatal("cycle not detected")
	}
}

func TestResolveProfileNotFound(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{}}
	_, _, err := cfg.ResolveProfile("nope")
	if err == nil {
		t.Fatal("missing profile not reported")
	}
}

func TestMaskValue(t *testing.T) {
	cases := []struct{ k, v string }{
		{"ANTHROPIC_API_KEY", "sk-ant-xxxxxxxxxxxxxx"},
		{"DB_PASSWORD", "supersecret123"},
		{"AUTH_TOKEN", "tok-xxxx"},
		{"CREDENTIAL", "cred-xxxx"},
	}
	for _, c := range cases {
		if got := MaskValue(c.k, c.v); got == c.v {
			t.Fatalf("MaskValue(%q) returned the value unchanged", c.k)
		}
	}
	if got := MaskValue("ANTHROPIC_MODEL", "claude-sonnet-5"); got != "claude-sonnet-5" {
		t.Fatalf("non-secret masked: got %q", got)
	}
	if got := MaskValue("API_KEY", "short"); got != "short" {
		t.Fatalf("short secret should pass through, got %q", got)
	}
}

func TestGlobalPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, want := GlobalPath(""), filepath.Join("/xdg", "enver", "config.yaml"); got != want {
		t.Fatalf("XDG path = %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/user")
	if got, want := GlobalPath(""), "/home/user/.config/enver/config.yaml"; got != want {
		t.Fatalf("HOME path = %q, want %q", got, want)
	}
	if got := GlobalPath("/custom/path.yaml"); got != "/custom/path.yaml" {
		t.Fatalf("override ignored: %q", got)
	}
}

func TestLoadMergedLayering(t *testing.T) {
	// Resolve the temp dir: on macOS t.TempDir() returns /var/folders/... but
	// os.Getwd() resolves the /var → /private/var symlink, which would break
	// the cwd-under-HOME prefix check in findLocal().
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	mkFile := func(rel, content string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkFile(".config/enver/config.yaml", "default: base\nprofiles:\n  p:\n    env:\n      K: base\n")
	proj := filepath.Join(home, "proj", "sub")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	// parent layer
	mkFile("proj/.enver.yaml", "profiles:\n  p:\n    env:\n      K: parent\n      EXTRA: parent\n")
	// closer layer
	mkFile("proj/sub/.enver.yaml", "profiles:\n  p:\n    env:\n      K: closer\n")

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	// From the deepest dir, the closer .enver.yaml wins for K; parent adds EXTRA.
	cwd := proj
	restore := chdir(t, cwd)
	defer restore()

	cfg, err := LoadMerged("", true)
	if err != nil {
		t.Fatal(err)
	}
	env, _, err := cfg.ResolveProfile("p")
	if err != nil {
		t.Fatal(err)
	}
	if env["K"] != "closer" {
		t.Fatalf("K = %q, want closer (nearest .enver.yaml)", env["K"])
	}
	if env["EXTRA"] != "parent" {
		t.Fatalf("EXTRA = %q, want parent (added by outer layer)", env["EXTRA"])
	}

	// --no-local falls back to global only.
	cfg2, err := LoadMerged("", false)
	if err != nil {
		t.Fatal(err)
	}
	env2, _, _ := cfg2.ResolveProfile("p")
	if env2["K"] != "base" {
		t.Fatalf("no-local K = %q, want base", env2["K"])
	}
}

func chdir(t *testing.T, dir string) func() {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chdir(wd) }
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mapEq(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestExtendedBy(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base":  {},
		"mid":   {Extends: "base"},
		"leaf":  {Extends: "mid"},
		"other": {Extends: "base"},
	}}
	got := cfg.ExtendedBy("base")
	want := []string{"mid", "other"}
	if !sliceEq(got, want) {
		t.Fatalf("ExtendedBy(base) = %v, want %v", got, want)
	}
	if got := cfg.ExtendedBy("leaf"); len(got) != 0 {
		t.Fatalf("ExtendedBy(leaf) = %v, want none", got)
	}
}

func TestResolveComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const yamlDoc = "profiles:\n" +
		"  base:\n" +
		"    env:\n" +
		"      # base foo\n" +
		"      FOO: base\n" +
		"      BAR: base\n" +
		"  mid:\n" +
		"    extends: base\n" +
		"    env:\n" +
		"      # mid foo\n" +
		"      FOO: mid\n" +
		"  leaf:\n" +
		"    extends: mid\n" +
		"    env:\n" +
		"      OWN: x\n"
	if err := os.WriteFile(path, []byte(yamlDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"FOO": "base", "BAR": "base"}},
		"mid":  {Extends: "base", Env: map[string]string{"FOO": "mid"}},
		"leaf": {Extends: "mid", Env: map[string]string{"OWN": "x"}},
	}}
	got, err := cfg.ResolveComments(path, "leaf")
	if err != nil {
		t.Fatalf("ResolveComments: %v", err)
	}
	// FOO: base comments first, mid overwrites (nearest-with-comment), leaf does
	// not define FOO — so the nearest commented definer is mid.
	if got["FOO"] != "mid foo" {
		t.Fatalf("FOO comment = %q, want %q", got["FOO"], "mid foo")
	}
	// BAR: defined only by base, with no comment → absent.
	if _, ok := got["BAR"]; ok {
		t.Fatalf("BAR should be absent (no comment), got %q", got["BAR"])
	}
	// OWN: defined by leaf, no comment → absent.
	if _, ok := got["OWN"]; ok {
		t.Fatalf("OWN should be absent (no comment), got %q", got["OWN"])
	}
}

// TestResolveCommentsMergedGlobalOnly mirrors TestResolveComments but through the
// merged resolver with local layering disabled: a regression guard that the
// single-file behavior is preserved.
func TestResolveCommentsMergedGlobalOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const yamlDoc = "profiles:\n" +
		"  base:\n" +
		"    env:\n" +
		"      # base foo\n" +
		"      FOO: base\n" +
		"      BAR: base\n" +
		"  mid:\n" +
		"    extends: base\n" +
		"    env:\n" +
		"      # mid foo\n" +
		"      FOO: mid\n" +
		"  leaf:\n" +
		"    extends: mid\n" +
		"    env:\n" +
		"      OWN: x\n"
	if err := os.WriteFile(path, []byte(yamlDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCommentsMerged(path, false, "leaf")
	if err != nil {
		t.Fatalf("ResolveCommentsMerged: %v", err)
	}
	if got["FOO"] != "mid foo" {
		t.Fatalf("FOO comment = %q, want %q", got["FOO"], "mid foo")
	}
	if _, ok := got["BAR"]; ok {
		t.Fatalf("BAR should be absent (no comment), got %q", got["BAR"])
	}
	if _, ok := got["OWN"]; ok {
		t.Fatalf("OWN should be absent (no comment), got %q", got["OWN"])
	}
}

// TestResolveCommentsMergedLocalWins verifies a comment from a closer .enver.yaml
// overlay overrides the global file's comment for the same key, matching how env
// values merge under LoadMerged.
func TestResolveCommentsMergedLocalWins(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	mkFile := func(rel, content string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkFile(".config/enver/config.yaml",
		"profiles:\n"+
			"  p:\n"+
			"    env:\n"+
			"      # global-cmt\n"+
			"      K: global\n")
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	mkFile("proj/.enver.yaml",
		"profiles:\n"+
			"  p:\n"+
			"    env:\n"+
			"      # local-cmt\n"+
			"      K: local\n")

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	restore := chdir(t, proj)
	defer restore()

	got, err := ResolveCommentsMerged("", true, "p")
	if err != nil {
		t.Fatalf("ResolveCommentsMerged: %v", err)
	}
	if got["K"] != "local-cmt" {
		t.Fatalf("K comment = %q, want %q (closer layer should win)", got["K"], "local-cmt")
	}

	// With local layering disabled, the global comment is used.
	gotGlobal, err := ResolveCommentsMerged("", false, "p")
	if err != nil {
		t.Fatalf("ResolveCommentsMerged (no-local): %v", err)
	}
	if gotGlobal["K"] != "global-cmt" {
		t.Fatalf("K comment = %q, want %q (global only)", gotGlobal["K"], "global-cmt")
	}
}

// TestResolveCommentsMergedMissingConfig verifies a missing global file does not
// panic: LoadMerged yields an empty Config and ResolveProfile reports the
// missing profile as an error.
func TestResolveCommentsMergedMissingConfig(t *testing.T) {
	got, err := ResolveCommentsMerged(filepath.Join(t.TempDir(), "missing.yaml"), false, "p")
	if err == nil {
		t.Fatalf("expected profile-not-found error for missing config, got map %v", got)
	}
	if got != nil {
		t.Fatalf("expected nil map on error, got %v", got)
	}
}
