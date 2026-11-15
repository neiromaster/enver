package config

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/envname"
	"gopkg.in/yaml.v3"
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
			"c": {Extends: Extends{"a"}, Env: map[string]string{"K5": "over"}},
		},
	}
	got := Merge(base, over, LayerGlobal, LayerLocal)
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
	if !got.Profiles["c"].Extends.Has("a") {
		t.Fatalf("c.extends = %q, want a", got.Profiles["c"].Extends)
	}
}

// Warnings describe the view a config was loaded as, so a fold does not carry
// them into a different view: LoadMerged computes the merged view's own after
// every fold.
func TestMergeDropsWarnings(t *testing.T) {
	base := Config{Warnings: []string{`remote "llm": content was fetched from elsewhere`}}
	over := Config{Warnings: []string{`remote "other": content was fetched from elsewhere`}}
	if got := Merge(base, over, LayerGlobal, LayerLocal).Warnings; got != nil {
		t.Fatalf("merged warnings = %q, want none", got)
	}
	if len(base.Warnings) != 1 {
		t.Fatalf("base warnings mutated: %q", base.Warnings)
	}
}

func TestMergeExtends(t *testing.T) {
	cases := []struct {
		name       string
		base, over Extends
		want       Extends
	}{
		{"dedup overlap", Extends{"a", "b"}, Extends{"b", "c"}, Extends{"a", "b", "c"}},
		{"empty over keeps base", Extends{"a"}, nil, Extends{"a"}},
		{"empty base takes over", nil, Extends{"x"}, Extends{"x"}},
		{"both empty", nil, nil, nil},
	}
	for _, c := range cases {
		got := mergeExtends(c.base, c.over)
		if !slices.Equal(got, c.want) {
			t.Fatalf("%s: mergeExtends(%v, %v) = %v, want %v", c.name, c.base, c.over, got, c.want)
		}
	}

	// A profile defined in both layers composes extends as [global…, local…].
	base := Config{Profiles: map[string]Profile{"p": {Extends: Extends{"g"}, Env: map[string]string{"K": "base"}}}}
	over := Config{Profiles: map[string]Profile{"p": {Extends: Extends{"l"}, Env: map[string]string{"K2": "over"}}}}
	merged := Merge(base, over, LayerGlobal, LayerLocal)
	if got := merged.Profiles["p"].Extends; !slices.Equal(got, Extends{"g", "l"}) {
		t.Fatalf("merged extends = %v, want [g l]", got)
	}

	// Local (later) parent wins over a global parent on a shared key.
	cfg := Config{Profiles: map[string]Profile{
		"g": {Env: map[string]string{"S": "global"}},
		"l": {Env: map[string]string{"S": "local"}},
		"p": {Extends: Extends{"g", "l"}},
	}}
	r, err := cfg.ResolveProfile("p")
	if err != nil {
		t.Fatal(err)
	}
	if r.Env["S"] != "local" {
		t.Fatalf("S = %q, want local (later parent wins)", r.Env["S"])
	}
}

func TestResolveProfileExtends(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"root": {Env: map[string]string{"A": "1", "B": "1"}},
		"mid":  {Extends: Extends{"root"}, Env: map[string]string{"B": "2", "C": "2"}},
		"leaf": {Extends: Extends{"mid"}, Env: map[string]string{"C": "3"}},
	}}
	r, err := cfg.ResolveProfile("leaf")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.Chain, []string{"leaf", "mid", "root"}; !slices.Equal(got, want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
	want := map[string]string{"A": "1", "B": "2", "C": "3"}
	if !mapEq(r.Env, want) {
		t.Fatalf("env = %v, want %v", r.Env, want)
	}
}

func TestResolveProfileCycle(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"a": {Extends: Extends{"b"}},
		"b": {Extends: Extends{"a"}},
	}}
	_, err := cfg.ResolveProfile("a")
	if !errors.Is(err, ErrExtendsCycle) {
		t.Fatalf("cycle not detected as ErrExtendsCycle: %v", err)
	}
}

func TestResolveProfileNotFound(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{}}
	_, err := cfg.ResolveProfile("nope")
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
		if got := MaskValue(c.v, ShouldMask(c.k, c.v, false, false)); got == c.v {
			t.Fatalf("MaskValue(%q) returned the value unchanged", c.k)
		}
	}
	if got := MaskValue("claude-sonnet-5", ShouldMask("ANTHROPIC_MODEL", "claude-sonnet-5", false, false)); got != "claude-sonnet-5" {
		t.Fatalf("non-secret masked: got %q", got)
	}
}

func TestShouldMask(t *testing.T) {
	if !ShouldMask("PROJECT_PATH", "/home/anna", true, false) {
		t.Fatal("marked masks")
	}
	if !ShouldMask("X", "enc:v3:abc", false, true) {
		t.Fatal("encrypted at rest masks")
	}
	if !ShouldMask("GITHUB_TOKEN", "x", false, false) {
		t.Fatal("heuristic still masks")
	}
	if ShouldMask("API", "https://api.example.com", false, false) {
		t.Fatal("a plain non-secret value must not mask")
	}
}

func TestMaskValueKeepsShortValueHidden(t *testing.T) {
	if got := MaskValue("hunter2", true); strings.Contains(got, "hunter2") {
		t.Fatalf("MaskValue leaked the value: %q", got)
	}
	if got := MaskValue("hunter2", false); got != "hunter2" {
		t.Fatalf("unmarked value changed: %q", got)
	}
}

// TestMaskValueShortSecretRedacted pins the length floor: a prefix shows only
// when it stays no more than a third of the value, so a short secret never leaks.
func TestMaskValueShortSecretRedacted(t *testing.T) {
	cases := []struct{ v, want string }{
		{"", ""},
		{"hunter2", "(len=7)"},
		{"password1", "(len=9)"},
		{"0123456789ab", "0123…" + "(len=12)"},
	}
	for _, c := range cases {
		if got := MaskValue(c.v, ShouldMask("API_KEY", c.v, false, false)); got != c.want {
			t.Errorf("MaskValue(API_KEY, %q) = %q, want %q", c.v, got, c.want)
		}
	}
}

func TestMaskValueURLSecrets(t *testing.T) {
	cases := []struct{ k, v string }{
		{"DATABASE_URL", "postgres://user:pass@db.internal:5432/app"},
		{"CONNECTION_STRING", "mysql://root:s3cr3t@host:3306/db"},
		{"REDIS_URL", "redis://:verysecret@cache:6379"},
		{"DATABASE_URL", "postgres://user:Abcdef/Xy@db.internal:5432/app"},
		{"GITHUB_URL", "https://ghp_xxx@github.com/org/repo"},
	}
	for _, c := range cases {
		if got := MaskValue(c.v, ShouldMask(c.k, c.v, false, false)); got == c.v {
			t.Fatalf("MaskValue(%q) should redact URL credentials", c.k)
		}
	}
	plain := []struct{ k, v string }{
		{"ANTHROPIC_BASE_URL", "https://api.anthropic.com"},
		{"DB_URL", "http://localhost:8082/health"},
		{"S3_ENDPOINT", "https://minio.example.com"},
		{"POSTGRES_URL", "postgres://db:5432/app"},
	}
	for _, c := range plain {
		if got := MaskValue(c.v, ShouldMask(c.k, c.v, false, false)); got != c.v {
			t.Fatalf("MaskValue(%q) must not redact a URL without credentials, got %q", c.k, got)
		}
	}
}

func TestShouldEncryptMatchesSecretSelection(t *testing.T) {
	cases := []struct {
		key, val string
		want     bool
	}{
		{"API_KEY", "sk-123", true},
		{"password", "hunter2", true},
		{"db", "postgres://u:p@host/db", true}, // URL credentials
		{"MODEL", "gpt-x", false},
		{"BASE_URL", "https://api.example.com", false},
	}
	for _, c := range cases {
		if got := ShouldEncrypt(c.key, c.val); got != c.want {
			t.Errorf("ShouldEncrypt(%q, %q) = %v, want %v", c.key, c.val, got, c.want)
		}
	}
}

func TestShouldEncryptMarkedArbitraryKey(t *testing.T) {
	if !ShouldEncrypt("~PROJECT_PATH", "/Users/anna/projects/enver") {
		t.Fatal("a marked key is a candidate whatever its name or value")
	}
	if ShouldEncrypt("PROJECT_PATH", "/Users/anna/projects/enver") {
		t.Fatal("the same key unmarked is not a candidate")
	}
}

func TestGlobalPath(t *testing.T) {
	// Absolute on every platform: a "/xdg" literal is not filepath.IsAbs on
	// Windows, so the spec-mandated ignore would kick in and test the wrong
	// branch.
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := GlobalPath(""), filepath.Join(xdg, "enver", "config.yaml"); got != want {
		t.Fatalf("XDG path = %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/user")
	if got, want := GlobalPath(""), filepath.Join("/home/user", ".config", "enver", "config.yaml"); got != want {
		t.Fatalf("HOME path = %q, want %q", got, want)
	}
	if got := GlobalPath("/custom/path.yaml"); got != "/custom/path.yaml" {
		t.Fatalf("override ignored: %q", got)
	}
}

func TestLoadMergedLayering(t *testing.T) {
	// Resolve the temp dir: on macOS t.TempDir() returns /var/folders/... but
	// os.Getwd() resolves the /var → /private/var symlink, which would break
	// path comparisons against cwd.
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

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	// Case 1: cwd has ./.enver.yaml → it is the local layer; local wins for K.
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	mkFile("proj/.enver.yaml", "profiles:\n  p:\n    env:\n      K: local\n")
	restore := chdir(t, proj)
	defer restore()

	cfg, err := LoadMerged("", true)
	if err != nil {
		t.Fatal(err)
	}
	r, err := cfg.ResolveProfile("p")
	if err != nil {
		t.Fatal(err)
	}
	if r.Env["K"] != "local" {
		t.Fatalf("K = %q, want local (cwd .enver.yaml)", r.Env["K"])
	}

	// --no-local falls back to global only.
	cfg2, err := LoadMerged("", false)
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := cfg2.ResolveProfile("p")
	if r2.Env["K"] != "base" {
		t.Fatalf("no-local K = %q, want base", r2.Env["K"])
	}

	// Case 2: cwd has no .enver.yaml but a parent dir does → the parent is NOT
	// walked; only global is loaded.
	sub := filepath.Join(proj, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	defer chdir(t, sub)()
	cfg3, err := LoadMerged("", true)
	if err != nil {
		t.Fatal(err)
	}
	r3, _ := cfg3.ResolveProfile("p")
	if r3.Env["K"] != "base" {
		t.Fatalf("K = %q, want base (parent .enver.yaml must not be walked)", r3.Env["K"])
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

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(p, []byte("default: a\nprofiles:\n  a:\n    env:\n      K: v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Default != "a" || cfg.Profiles["a"].Env["K"] != "v" {
		t.Fatalf("LoadFile parsed wrong: %+v", cfg)
	}
	// Missing file → empty Config, no error.
	cfg2, err := LoadFile(filepath.Join(dir, "missing.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(cfg2.Profiles) != 0 || cfg2.Default != "" {
		t.Fatalf("missing file should yield empty Config: %+v", cfg2)
	}
}

func TestExtendedBy(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base":  {},
		"mid":   {Extends: Extends{"base"}},
		"leaf":  {Extends: Extends{"mid"}},
		"other": {Extends: Extends{"base"}},
	}}
	got := cfg.ExtendedBy("base")
	want := []string{"mid", "other"}
	if !slices.Equal(got, want) {
		t.Fatalf("ExtendedBy(base) = %v, want %v", got, want)
	}
	if got := cfg.ExtendedBy("leaf"); len(got) != 0 {
		t.Fatalf("ExtendedBy(leaf) = %v, want none", got)
	}
}

func TestResolveCommentsChainFold(t *testing.T) {
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
	cfg, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := cfg.ResolveProfile("leaf")
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	// FOO: base comments first, mid overwrites (nearest-with-comment), leaf
	// does not define FOO — so the nearest commented definer is mid.
	if r.Comments["FOO"] != "mid foo" {
		t.Fatalf("FOO comment = %q, want %q", r.Comments["FOO"], "mid foo")
	}
	if _, ok := r.Comments["BAR"]; ok {
		t.Fatalf("BAR should be absent (no comment), got %q", r.Comments["BAR"])
	}
	if _, ok := r.Comments["OWN"]; ok {
		t.Fatalf("OWN should be absent (no comment), got %q", r.Comments["OWN"])
	}
	if r.Chain[0] != "leaf" || len(r.Chain) != 3 {
		t.Fatalf("chain = %v, want [leaf mid base]", r.Chain)
	}
}

// TestResolveCommentsMergedGlobalOnly mirrors TestResolveCommentsChainFold but through the
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
	cfg, err := LoadMerged(path, false)
	if err != nil {
		t.Fatal(err)
	}
	r, err := cfg.ResolveProfile("leaf")
	if err != nil {
		t.Fatal(err)
	}
	if r.Comments["FOO"] != "mid foo" {
		t.Fatalf("FOO comment = %q, want %q", r.Comments["FOO"], "mid foo")
	}
	if _, ok := r.Comments["BAR"]; ok {
		t.Fatalf("BAR should be absent (no comment), got %q", r.Comments["BAR"])
	}
	if _, ok := r.Comments["OWN"]; ok {
		t.Fatalf("OWN should be absent (no comment), got %q", r.Comments["OWN"])
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

	cfg, err := LoadMerged("", true)
	if err != nil {
		t.Fatal(err)
	}
	r, err := cfg.ResolveProfile("p")
	if err != nil {
		t.Fatal(err)
	}
	if r.Comments["K"] != "local-cmt" {
		t.Fatalf("K comment = %q, want %q (closer layer should win)", r.Comments["K"], "local-cmt")
	}

	// With local layering disabled, the global comment is used.
	cfgG, err := LoadMerged("", false)
	if err != nil {
		t.Fatal(err)
	}
	rG, err := cfgG.ResolveProfile("p")
	if err != nil {
		t.Fatal(err)
	}
	if rG.Comments["K"] != "global-cmt" {
		t.Fatalf("K comment = %q, want %q (global only)", rG.Comments["K"], "global-cmt")
	}
}

// TestEditProbeResolvesCommentsAlongWorkingChain verifies an in-memory probe
// (simulating an in-progress edit) resolves comments along its own chain:
// dev now extends other, not base, so FOO takes the other comment.
func TestEditProbeResolvesCommentsAlongWorkingChain(t *testing.T) {
	probe := Config{Profiles: map[string]Profile{
		"base":  {Env: map[string]string{"FOO": "bv"}, Comments: map[string]string{"FOO": "base cmt"}},
		"other": {Env: map[string]string{"FOO": "ov"}, Comments: map[string]string{"FOO": "other cmt"}},
		"dev":   {Extends: Extends{"other"}, Env: map[string]string{"OWN": "x"}},
	}}
	r, err := probe.ResolveProfile("dev")
	if err != nil {
		t.Fatal(err)
	}
	if r.Comments["FOO"] != "other cmt" {
		t.Fatalf("FOO comment = %q, want %q (probe chain dev->other)", r.Comments["FOO"], "other cmt")
	}
}

// TestResolveMissingConfigErrorsOnProfile verifies a missing global file does not
// panic: LoadMerged yields an empty Config and ResolveProfile reports the
// missing profile as an error.
func TestResolveMissingConfigErrorsOnProfile(t *testing.T) {
	cfg, err := LoadMerged(filepath.Join(t.TempDir(), "missing.yaml"), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.ResolveProfile("p"); err == nil {
		t.Fatal("expected profile-not-found error for missing config")
	}
}

func TestResolveProfileMultipleExtends(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base":   {Env: map[string]string{"A": "base", "B": "base"}},
		"trait1": {Extends: Extends{"base"}, Env: map[string]string{"B": "t1", "C": "t1"}},
		"trait2": {Extends: Extends{"base"}, Env: map[string]string{"C": "t2", "D": "t2"}},
		"mix":    {Extends: Extends{"trait1", "trait2"}, Env: map[string]string{"D": "own"}},
	}}
	r, err := cfg.ResolveProfile("mix")
	if err != nil {
		t.Fatal(err)
	}
	// trait2 is later than trait1: B=base (trait2's base-inherited B overwrites trait1's B=t1),
	// C=t2 (trait2's direct definition wins), D=own (child wins). base unchanged: A=base.
	// This is the diamond behavior: later parent's full env (including inherited keys) overwrites earlier.
	want := map[string]string{"A": "base", "B": "base", "C": "t2", "D": "own"}
	if !mapEq(r.Env, want) {
		t.Fatalf("env = %v, want %v", r.Env, want)
	}
	if got, want := r.Chain, []string{"mix", "trait1", "base", "trait2"}; !slices.Equal(got, want) {
		t.Fatalf("chain = %v, want %v", got, want)
	}
}

func TestResolveProfileMultiCycle(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"a": {Extends: Extends{"b", "c"}},
		"b": {Extends: Extends{"c"}},
		"c": {Extends: Extends{"a"}},
	}}
	if _, err := cfg.ResolveProfile("a"); err == nil {
		t.Fatal("multi-parent cycle not detected")
	}
}

func TestExtendsHas(t *testing.T) {
	e := Extends{"a", "b"}
	if !e.Has("a") || !e.Has("b") || e.Has("c") {
		t.Fatalf("Has wrong for %v", e)
	}
	if (Extends{}).Has("a") {
		t.Fatal("empty Extends reports Has")
	}
}

func TestExtendsYAMLScalarAndList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	// list form
	if err := os.WriteFile(path, []byte("profiles:\n  mix:\n    extends: [a, b]\n    env:\n      K: v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Profiles["mix"].Extends; !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("list extends = %v, want [a b]", got)
	}
	// scalar form still works
	if err := os.WriteFile(path, []byte("profiles:\n  one:\n    extends: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ = load(path)
	if got := cfg.Profiles["one"].Extends; !slices.Equal(got, []string{"a"}) {
		t.Fatalf("scalar extends = %v, want [a]", got)
	}
}

func TestResolveCommentsMixinOverlap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yml := "profiles:\n" +
		"  a:\n    env:\n      # from a\n      K: av\n" +
		"  b:\n    env:\n      # from b\n      K: bv\n" +
		"  self:\n    extends: [a, b]\n"
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := cfg.ResolveProfile("self")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.Env["K"], "bv"; got != want {
		t.Fatalf("value K = %q, want %q", got, want)
	}
	if got, want := r.Comments["K"], "from b"; got != want {
		t.Fatalf("comment K = %q, want %q (value source is b)", got, want)
	}
}

// TestCommentProvenanceMatchesValueProvenance pins the approved delta: for a
// key defined in both a local parent profile and the global self profile, the
// value comes from global self (chain position dominates layers) and the
// comment must now come from the same definer. The old file-major comment
// walk returned the local parent comment here.
func TestCommentProvenanceMatchesValueProvenance(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "global.yaml")
	local := filepath.Join(dir, "local.yaml")
	if err := os.WriteFile(global, []byte("profiles:\n"+
		"  p1:\n    env:\n      # from global p1\n      FOO: a\n"+
		"  self:\n    extends: p1\n    env:\n      # from global self\n      FOO: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("profiles:\n"+
		"  p1:\n    env:\n      # from local p1\n      FOO: c\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := load(global)
	if err != nil {
		t.Fatal(err)
	}
	l, err := load(local)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Merge(g, l, LayerGlobal, LayerLocal).ResolveProfile("self")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.Env["FOO"], "b"; got != want {
		t.Fatalf("value = %q, want %q", got, want)
	}
	if got, want := r.Comments["FOO"], "from global self"; got != want {
		t.Fatalf("comment = %q, want %q (provenance must match value)", got, want)
	}
}

func TestProfileCommentsExtractedAtDecode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yml := "profiles:\n" +
		"  p:\n" +
		"    env:\n" +
		"      # first line\n" +
		"      # second\n" +
		"      A: 1\n" +
		"      B: 2\n" +
		"  q: {}\n"
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Profiles["p"]
	if got, want := p.Comments["A"], "first line\n# second"; got != want {
		t.Fatalf("A comment = %q, want %q", got, want)
	}
	if _, ok := p.Comments["B"]; ok {
		t.Fatalf("B should have no comment, got %q", p.Comments["B"])
	}
	if cfg.Profiles["q"].Comments != nil {
		t.Fatalf("profile without env should have nil Comments, got %v", cfg.Profiles["q"].Comments)
	}
}

func TestProfileCommentsCommentWithoutSpace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yml := "profiles:\n  p:\n    env:\n      #nospace\n      A: 1\n"
	if err := os.WriteFile(path, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Profiles["p"].Comments["A"]; got != "#nospace" {
		t.Fatalf("comment without space must be kept verbatim, got %q", got)
	}
}

func TestMergeCommentsSticky(t *testing.T) {
	base := Config{Profiles: map[string]Profile{
		"p": {
			Env:      map[string]string{"A": "1", "B": "2"},
			Comments: map[string]string{"A": "global a", "B": "global b"},
		},
	}}
	over := Config{Profiles: map[string]Profile{
		"p": {
			Env:      map[string]string{"A": "x", "C": "3"},
			Comments: map[string]string{"C": "local c"},
		},
	}}
	got := Merge(base, over, LayerGlobal, LayerLocal).Profiles["p"]
	if v := got.Comments["A"]; v != "global a" {
		t.Fatalf("A comment = %q, want %q (redefined without comment keeps base)", v, "global a")
	}
	if v := got.Comments["B"]; v != "global b" {
		t.Fatalf("B comment = %q, want %q (untouched key keeps base)", v, "global b")
	}
	if v := got.Comments["C"]; v != "local c" {
		t.Fatalf("C comment = %q, want %q (new commented key)", v, "local c")
	}
	if v := got.Env["A"]; v != "x" {
		t.Fatalf("A value = %q, want %q (values always overwrite)", v, "x")
	}
}

func TestEncryptFileRefusesKeyMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfgContent := "profiles:\n  p:\n    env:\n      TOKEN: plain\n"
	if err := os.WriteFile(path, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	saltA := []byte("aaaaaaaaaaaaaaaa")
	saltB := []byte("bbbbbbbbbbbbbbbb")
	if n, err := EncryptFile(path, key, saltA, "", false); err != nil || n != 1 {
		t.Fatalf("first encrypt: n=%d err=%v, want 1/nil", n, err)
	}
	n, err := EncryptFile(path, key, saltB, "", false)
	if err == nil || !strings.Contains(err.Error(), "different key") {
		t.Fatalf("second encrypt with mismatched salt: err=%v, want refusal", err)
	}
	if n != 0 {
		t.Fatalf("n = %d, want 0 (nothing written on refusal)", n)
	}
	data, _ := os.ReadFile(path)
	if strings.Count(string(data), "enc:v3:") != 1 {
		t.Fatal("file must keep exactly one encrypted value")
	}
}

func TestEncryptFileRejectsForeignEnc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfgContent := "profiles:\n  p:\n    env:\n      TOKEN: enc:v2:YWJj\n"
	if err := os.WriteFile(path, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	salt := make([]byte, 16)
	n, err := EncryptFile(path, key, salt, "", true)
	if err == nil || !strings.Contains(err.Error(), "unsupported encrypted value") {
		t.Fatalf("err = %v, want unsupported encrypted value", err)
	}
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "enc:v2:YWJj") || strings.Contains(string(data), "enc:v3:") {
		t.Fatal("file must be left untouched (no double encryption)")
	}
}

func TestDecryptFileRejectsForeignEnc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	cfgContent := "profiles:\n  p:\n    env:\n      TOKEN: enc:v2:YWJj\n"
	if err := os.WriteFile(path, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	n, err := DecryptFile(path, key, "")
	if err == nil || !strings.Contains(err.Error(), "unsupported encrypted value") {
		t.Fatalf("err = %v, want unsupported encrypted value (not fake success)", err)
	}
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
}

func TestLoadRejectsInvalidEnvNames(t *testing.T) {
	for name, body := range map[string]string{
		"env key":           "profiles:\n  p:\n    env:\n      K; touch /tmp/pwned: v\n",
		"unset":             "profiles:\n  p:\n    unset: [\"K; touch /tmp/pwned\"]\n",
		"leading digit key": "profiles:\n  p:\n    env:\n      1KEY: v\n",
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(path); err == nil {
			t.Errorf("%s: load must reject invalid names loudly", name)
		}
	}
}

func TestMergeCaseVariantOverlay(t *testing.T) {
	base := Config{Profiles: map[string]Profile{"p": {Env: map[string]string{"PATH": "g"}}}}
	over := Config{Profiles: map[string]Profile{"p": {Env: map[string]string{"Path": "l"}}}}
	got := Merge(base, over, LayerGlobal, LayerLocal).Profiles["p"].Env
	if runtime.GOOS == "windows" {
		if len(got) != 1 || got["Path"] != "l" {
			t.Fatalf("merged env = %v, want the local variant only: {Path: l}", got)
		}
	} else if len(got) != 2 {
		t.Fatalf("merged env = %v, want both spellings on POSIX", got)
	}
}

func TestResolveCaseVariantChildWins(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base":  {Env: map[string]string{"PATH": "g"}},
		"child": {Extends: Extends{"base"}, Env: map[string]string{"path": "l"}},
	}}
	r, err := cfg.ResolveProfile("child")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if len(r.Env) != 1 || r.Env["path"] != "l" {
			t.Fatalf("resolved env = %v, want {path: l}", r.Env)
		}
		if len(r.Sources) != 1 || r.Sources["path"].Profile != "child" {
			t.Fatalf("sources = %v, want the stale base entry collapsed too", r.Sources)
		}
	} else if len(r.Env) != 2 {
		t.Fatalf("resolved env = %v, want both spellings on POSIX", r.Env)
	}
}

func TestUnsetDedupCaseVariants(t *testing.T) {
	got := appendUniq([]string{"PATH"}, "Path")
	want := 2
	if runtime.GOOS == "windows" {
		want = 1
	}
	if len(got) != want {
		t.Fatalf("appendUniq = %v, want %d entries", got, want)
	}
}

// TestMergeExtendsKeepsCaseDistinctNames pins byte-exact extends dedup:
// profile names are case-sensitive on every platform, so [prod] and [Prod]
// compose rather than collapse.
func TestMergeExtendsKeepsCaseDistinctNames(t *testing.T) {
	base := Config{Profiles: map[string]Profile{"c": {Extends: Extends{"prod"}}}}
	over := Config{Profiles: map[string]Profile{"c": {Extends: Extends{"Prod"}}}}
	got := Merge(base, over, LayerGlobal, LayerLocal).Profiles["c"].Extends
	if !reflect.DeepEqual([]string(got), []string{"prod", "Prod"}) {
		t.Fatalf("extends = %v, want [prod Prod] (names are case-sensitive everywhere)", got)
	}
}

// TestMergeDoesNotMutateInputs pins the purity contract: Merge writes into
// clones, so callers may reuse base or override as the pre-merge view
// afterwards, including provenance maps filled by earlier merges.
func TestMergeDoesNotMutateInputs(t *testing.T) {
	base := Config{Profiles: map[string]Profile{
		"shared": {Env: map[string]string{"A": "1", "B": "2"}, Unset: Unsets{"B"}},
		"quiet":  {Env: map[string]string{"C": "3"}},
	},
		Origins:      map[string]map[string]string{"shared": {"A": LayerGlobal}},
		UnsetOrigins: map[string]map[string]string{"shared": {"B": LayerGlobal}},
		ProfileLayer: map[string]string{"shared": LayerGlobal, "quiet": LayerGlobal},
	}
	override := Config{Profiles: map[string]Profile{
		"shared": {Env: map[string]string{"A": "9"}, Unset: Unsets{"A"}},
	}}

	merged := Merge(base, override, LayerGlobal, LayerLocal)

	// Base profiles unchanged.
	if !maps.Equal(base.Profiles["shared"].Env, map[string]string{"A": "1", "B": "2"}) {
		t.Errorf("base shared.Env mutated: %v", base.Profiles["shared"].Env)
	}
	if !slices.Equal(base.Profiles["shared"].Unset, Unsets{"B"}) {
		t.Errorf("base shared.Unset mutated: %v", base.Profiles["shared"].Unset)
	}
	if !maps.Equal(base.Profiles["quiet"].Env, map[string]string{"C": "3"}) {
		t.Errorf("base quiet.Env mutated: %v", base.Profiles["quiet"].Env)
	}

	// Base provenance unchanged.
	if !maps.Equal(base.Origins["shared"], map[string]string{"A": LayerGlobal}) {
		t.Errorf("base.Origins[shared] mutated: %v", base.Origins["shared"])
	}
	if !maps.Equal(base.UnsetOrigins["shared"], map[string]string{"B": LayerGlobal}) {
		t.Errorf("base.UnsetOrigins[shared] mutated: %v", base.UnsetOrigins["shared"])
	}
	if !maps.Equal(base.ProfileLayer, map[string]string{"shared": LayerGlobal, "quiet": LayerGlobal}) {
		t.Errorf("base.ProfileLayer mutated: %v", base.ProfileLayer)
	}

	// Override unchanged.
	if !maps.Equal(override.Profiles["shared"].Env, map[string]string{"A": "9"}) {
		t.Errorf("override shared.Env mutated: %v", override.Profiles["shared"].Env)
	}
	if !slices.Equal(override.Profiles["shared"].Unset, Unsets{"A"}) {
		t.Errorf("override shared.Unset mutated: %v", override.Profiles["shared"].Unset)
	}

	// Merged result correctness.
	if v := merged.Profiles["shared"].Env["A"]; v != "9" {
		t.Errorf("merged shared.A = %q, want 9", v)
	}
	if _, ok := merged.Profiles["shared"].Env["B"]; ok {
		t.Errorf("merged shared.B survived the inherited fence: %v", merged.Profiles["shared"].Env)
	}
}

// TestMergeThreeLayerFences pins the carried-fence gate across the three-layer
// fold: a fence strips every mention from its own era or lower and goes silent
// the moment a higher layer owns the key. The positional stamping this test
// replaces mislabeled global content as local under a remote base, so the gate
// misfired and the fenced value resurrected — the case the era-ranked gate
// exists to keep fenced.
func TestMergeThreeLayerFences(t *testing.T) {
	const remoteLabel = "remote:llm"
	fold := func(remote, global, local Config) Config {
		return Merge(Merge(remote, global, remoteLabel, LayerGlobal), local, LayerGlobal, LayerLocal)
	}
	remoteFence := Config{Profiles: map[string]Profile{
		"vic":  {Env: map[string]string{"K": "r"}},
		"leaf": {Extends: Extends{"vic"}, Unset: Unsets{"K"}},
	}}
	globalFence := Config{Profiles: map[string]Profile{
		"vic":  {Env: map[string]string{"K": "g"}},
		"leaf": {Extends: Extends{"vic"}, Unset: Unsets{"K"}},
	}}
	cases := []struct {
		name       string
		remote     Config
		global     Config
		local      Config
		wantK      string // "" means fenced: K absent with a tombstone
		wantFence  string // expected Unsets[K].Layer when K is fenced
		wantSource string // expected Sources[K].Layer when K is exported
	}{
		{
			name:      "remote fence fences a remote victim",
			remote:    remoteFence,
			wantK:     "",
			wantFence: remoteLabel,
		},
		{
			name:   "remote fence goes silent before a global redefinition",
			remote: remoteFence,
			global: Config{Profiles: map[string]Profile{"vic": {Env: map[string]string{"K": "g"}}}},
			wantK:  "g", wantSource: LayerGlobal,
		},
		{
			name:   "remote fence goes silent before a local redefinition",
			remote: remoteFence,
			local:  Config{Profiles: map[string]Profile{"vic": {Env: map[string]string{"K": "l"}}}},
			wantK:  "l", wantSource: LayerLocal,
		},
		{
			name:   "global fence fences a global extends victim over a remote base",
			remote: Config{Profiles: map[string]Profile{"unref": {Env: map[string]string{"X": "r"}}}},
			global: globalFence,
			wantK:  "", wantFence: LayerGlobal,
		},
		{
			name:   "global fence goes silent before a local redefinition",
			global: globalFence,
			local:  Config{Profiles: map[string]Profile{"vic": {Env: map[string]string{"K": "l"}}}},
			wantK:  "l", wantSource: LayerLocal,
		},
	}
	for _, tc := range cases {
		r, err := fold(tc.remote, tc.global, tc.local).ResolveProfile("leaf")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if tc.wantK == "" {
			if v, ok := r.Env["K"]; ok {
				t.Errorf("%s: K = %q exported, want fenced: %v", tc.name, v, r.Env)
			}
			if s := r.Unsets["K"]; s != (Source{Profile: "leaf", Layer: tc.wantFence}) {
				t.Errorf("%s: K tombstone = %+v, want {leaf %s}", tc.name, s, tc.wantFence)
			}
			continue
		}
		if v := r.Env["K"]; v != tc.wantK {
			t.Errorf("%s: K = %q, want %q: the user redefinition must win", tc.name, v, tc.wantK)
		}
		if s := r.Sources["K"]; s.Layer != tc.wantSource {
			t.Errorf("%s: K source = %+v, want layer %s", tc.name, s, tc.wantSource)
		}
		if _, ok := r.Unsets["K"]; ok {
			t.Errorf("%s: K carries a tombstone next to a live value: %v", tc.name, r.Unsets)
		}
	}

	// Invariant: a remote base must not change the resolution of a chain that
	// never references remote content — the three-layer fold must answer what
	// the two-layer fold answers, sources and tombstones included.
	invGlobal := Config{Profiles: map[string]Profile{
		"vic":   {Env: map[string]string{"K": "g"}},
		"leaf":  {Extends: Extends{"vic"}, Unset: Unsets{"K"}},
		"plain": {Extends: Extends{"vic"}},
	}}
	invLocal := Config{Profiles: map[string]Profile{"other": {Env: map[string]string{"Y": "l"}}}}
	invRemote := Config{Profiles: map[string]Profile{"unref": {Env: map[string]string{"X": "r"}}}}
	for _, name := range []string{"leaf", "plain"} {
		two, err := Merge(invGlobal, invLocal, LayerGlobal, LayerLocal).ResolveProfile(name)
		if err != nil {
			t.Fatal(err)
		}
		three, err := fold(invRemote, invGlobal, invLocal).ResolveProfile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !maps.Equal(two.Env, three.Env) || !maps.Equal(two.Unsets, three.Unsets) ||
			!reflect.DeepEqual(two.Sources, three.Sources) {
			t.Errorf("%s: remote base changed resolution: two-layer %v/%v, three-layer %v/%v",
				name, two.Env, two.Unsets, three.Env, three.Unsets)
		}
	}
}

// TestMergeProfileLayerStamping pins the per-profile declaring layer across the
// nested fold: an overriding copy restamps the name, a carried entry keeps its
// layer, and a remote-only profile must not decay to the outer fold's base
// label.
func TestMergeProfileLayerStamping(t *testing.T) {
	const remoteLabel = "remote:llm"
	remote := Config{Profiles: map[string]Profile{
		"only": {Env: map[string]string{"A": "r"}},
		"both": {Env: map[string]string{"B": "r"}},
	}}
	global := Config{Profiles: map[string]Profile{
		"both": {Env: map[string]string{"B": "g"}},
		"gn":   {Env: map[string]string{"C": "g"}},
	}}
	local := Config{Profiles: map[string]Profile{"ln": {Env: map[string]string{"D": "l"}}}}
	merged := Merge(Merge(remote, global, remoteLabel, LayerGlobal), local, LayerGlobal, LayerLocal)
	want := map[string]string{
		"only": remoteLabel,
		"both": LayerGlobal,
		"gn":   LayerGlobal,
		"ln":   LayerLocal,
	}
	if !maps.Equal(merged.ProfileLayer, want) {
		t.Fatalf("ProfileLayer = %v, want %v", merged.ProfileLayer, want)
	}
}

// TestMergeExtendsOriginsStamping pins the authorship of each extends target
// across the fold: projection attributes the references a remote's file
// declares to that remote, a user patch takes over the profile name without
// inheriting authorship of references it did not write, and a target the patch
// writes itself is the patch's.
func TestMergeExtendsOriginsStamping(t *testing.T) {
	remote := Project(Config{Profiles: map[string]Profile{
		"ollama": {Env: map[string]string{"OLLAMA_HOST": "localhost"}},
		"qwen":   {Extends: Extends{"ollama", "base"}},
	}}, "llm", false)
	wantRemote := map[string]string{
		"llm/ollama": RemoteLayer("llm"),
		"base":       RemoteLayer("llm"),
	}
	if got := remote.ExtendsOrigins["llm/qwen"]; !maps.Equal(got, wantRemote) {
		t.Fatalf("projected ExtendsOrigins = %v, want %v", got, wantRemote)
	}

	patch := Config{Profiles: map[string]Profile{
		"llm/qwen": {Extends: Extends{"mine"}, Env: map[string]string{"MODEL": "mine"}},
	}}
	merged := Merge(remote, patch, RemoteLayer("llm"), LayerGlobal)
	want := map[string]string{
		"llm/ollama": RemoteLayer("llm"),
		"base":       RemoteLayer("llm"),
		"mine":       LayerGlobal,
	}
	if got := merged.ExtendsOrigins["llm/qwen"]; !maps.Equal(got, want) {
		t.Fatalf("patched ExtendsOrigins = %v, want %v", got, want)
	}
	if got := merged.ProfileLayer["llm/qwen"]; got != LayerGlobal {
		t.Fatalf("ProfileLayer = %q, want the patch to own the name", got)
	}
	if got := merged.ExtendsOrigins["llm/ollama"]; got != nil {
		t.Fatalf("ExtendsOrigins[llm/ollama] = %v, want no entries for a profile without extends", got)
	}
}

func TestLayerEra(t *testing.T) {
	cases := []struct {
		layer string
		want  int
	}{
		{"remote:llm", 0},
		{"remote:", 0},
		{LayerGlobal, 1},
		{"", 1},
		{LayerLocal, 2},
	}
	for _, tc := range cases {
		if got := LayerEra(tc.layer); got != tc.want {
			t.Errorf("LayerEra(%q) = %d, want %d", tc.layer, got, tc.want)
		}
	}
}

func TestUnmarshalStripsMarkAndRecordsIt(t *testing.T) {
	var c Config
	err := yaml.Unmarshal([]byte(`profiles:
  work:
    env:
      # note
      ~PROJECT_PATH: /home/anna
      API: https://api.example.com
`), &c)
	if err != nil {
		t.Fatal(err)
	}
	p := c.Profiles["work"]
	if p.Env["PROJECT_PATH"] != "/home/anna" || p.Env["~PROJECT_PATH"] != "" {
		t.Fatalf("env not stripped: %#v", p.Env)
	}
	if !p.Marked["PROJECT_PATH"] || p.Marked["API"] {
		t.Fatalf("marks wrong: %#v", p.Marked)
	}
	if p.Comments["PROJECT_PATH"] != "note" {
		t.Fatalf("comment not keyed by the stripped name: %#v", p.Comments)
	}
	if _, ok := p.Comments["~PROJECT_PATH"]; ok {
		t.Fatalf("comment kept the raw key: %#v", p.Comments)
	}
}

func TestUnmarshalRejectsCollidingSpellings(t *testing.T) {
	var c Config
	err := yaml.Unmarshal([]byte(`profiles:
  work:
    env:
      ~TOKEN: a
      TOKEN: b
`), &c)
	if err == nil || !strings.Contains(err.Error(), "same variable") {
		t.Fatalf("want collision error, got %v", err)
	}
}

func TestUnmarshalRejectsBareMark(t *testing.T) {
	var c Config
	err := yaml.Unmarshal([]byte("profiles:\n  work:\n    env:\n      ~: x\n"), &c)
	if err == nil || !strings.Contains(err.Error(), "no name") {
		t.Fatalf("want a mark-without-name error, got %v", err)
	}
}

func TestUnmarshalRejectsDoubledMark(t *testing.T) {
	var c Config
	err := yaml.Unmarshal([]byte("profiles:\n  work:\n    env:\n      ~~KEY: x\n"), &c)
	if err == nil || !strings.Contains(err.Error(), "doubled") {
		t.Fatalf("want a doubled-mark error, got %v", err)
	}
}

func TestUnmarshalNormalizesUnsetMark(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("profiles:\n  work:\n    unset: [~TOKEN]\n"), &c); err != nil {
		t.Fatal(err)
	}
	if got := c.Profiles["work"].Unset; len(got) != 1 || got[0] != "TOKEN" {
		t.Fatalf("unset not normalized: %#v", got)
	}
}

func TestUnmarshalCollapsesUnsetMarkAndPlainSpellings(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("profiles:\n  work:\n    unset: [~TOKEN, TOKEN]\n"), &c); err != nil {
		t.Fatal(err)
	}
	if got := c.Profiles["work"].Unset; len(got) != 1 || got[0] != "TOKEN" {
		t.Fatalf("unset mark and plain spellings not collapsed: %#v", got)
	}
}

func TestUnmarshalLeavesMarkedNilWithoutMarks(t *testing.T) {
	var c Config
	if err := yaml.Unmarshal([]byte("profiles:\n  work:\n    env:\n      API: x\n"), &c); err != nil {
		t.Fatal(err)
	}
	if got := c.Profiles["work"].Marked; got != nil {
		t.Fatalf("Marked should be nil without marks, got %#v", got)
	}
}

func TestUnmarshalResolvesAliasedEnv(t *testing.T) {
	var c Config
	err := yaml.Unmarshal([]byte(`common: &common
  ~TOKEN: a
  B: b
profiles:
  work:
    env: *common
`), &c)
	if err != nil {
		t.Fatal(err)
	}
	p := c.Profiles["work"]
	if p.Env["TOKEN"] != "a" || p.Env["B"] != "b" || p.Env["~TOKEN"] != "" {
		t.Fatalf("aliased env not decoded and stripped: %#v", p.Env)
	}
	if !p.Marked["TOKEN"] {
		t.Fatalf("mark inside the anchor lost: %#v", p.Marked)
	}
}

func TestUnmarshalResolvesMergedEnvKeys(t *testing.T) {
	var c Config
	err := yaml.Unmarshal([]byte(`common: &common
  ~TOKEN: a
  C: old
profiles:
  work:
    env:
      <<: *common
      C: "3"
`), &c)
	if err != nil {
		t.Fatal(err)
	}
	p := c.Profiles["work"]
	if p.Env["TOKEN"] != "a" || p.Env["C"] != "3" {
		t.Fatalf("merged env wrong: %#v", p.Env)
	}
	if !p.Marked["TOKEN"] || p.Marked["C"] {
		t.Fatalf("merged marks wrong: %#v", p.Marked)
	}
}

func TestUnmarshalResolvesProfileLevelMerge(t *testing.T) {
	var c Config
	err := yaml.Unmarshal([]byte(`base: &base
  env:
    ~TOKEN: a
profiles:
  work:
    <<: *base
`), &c)
	if err != nil {
		t.Fatal(err)
	}
	p := c.Profiles["work"]
	if p.Env["TOKEN"] != "a" {
		t.Fatalf("profile-level merge env lost: %#v", p.Env)
	}
	if !p.Marked["TOKEN"] {
		t.Fatalf("profile-level merge mark lost: %#v", p.Marked)
	}
}

func TestUnmarshalMergeSourcePrecedence(t *testing.T) {
	var c Config
	err := yaml.Unmarshal([]byte(`a: &a
  X: 1
b: &b
  Y: 2
  X: 9
profiles:
  work:
    env:
      <<: [*a, *b]
`), &c)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Profiles["work"].Env; got["X"] != "1" || got["Y"] != "2" {
		t.Fatalf("merge precedence wrong (an earlier source wins): %#v", got)
	}
}

func TestUnmarshalExplicitKeyOverridesMergedCopy(t *testing.T) {
	var c Config
	err := yaml.Unmarshal([]byte(`a: &a
  ~X: 1
  Y: 1
profiles:
  plain:
    env:
      <<: *a
      X: 2
  marked:
    env:
      <<: *a
      ~Y: 2
`), &c)
	if err != nil {
		t.Fatal(err)
	}
	plain := c.Profiles["plain"]
	if plain.Env["X"] != "2" {
		t.Fatalf("explicit key must override the merged copy: %#v", plain.Env)
	}
	if plain.Marked["X"] {
		t.Fatalf("a plain override must clear the merged mark: %#v", plain.Marked)
	}
	if plain.Marked != nil {
		t.Fatalf("a profile left with no marks must hold nil Marked: %#v", plain.Marked)
	}
	marked := c.Profiles["marked"]
	if marked.Env["Y"] != "2" || !marked.Marked["Y"] {
		t.Fatalf("a marked override must win with its mark: env=%#v marks=%#v", marked.Env, marked.Marked)
	}
}

func TestUnmarshalRejectsDoubledMarkWithoutName(t *testing.T) {
	var c Config
	err := yaml.Unmarshal([]byte("profiles:\n  work:\n    env:\n      ~~: x\n"), &c)
	if err == nil || !strings.Contains(err.Error(), "doubled") {
		t.Fatalf("want a doubled-mark error, got %v", err)
	}
}

func TestUnmarshalRejectsCaseVariantCollisionOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("env names are case-sensitive elsewhere")
	}
	var c Config
	err := yaml.Unmarshal([]byte("profiles:\n  work:\n    env:\n      ~Path: a\n      PATH: b\n"), &c)
	if err == nil || !strings.Contains(err.Error(), "same variable") {
		t.Fatalf("want collision error, got %v", err)
	}
}

func TestUnmarshalRejectsEnvKeyItCannotWalk(t *testing.T) {
	var c Config
	err := yaml.Unmarshal([]byte("e: &e env\nprofiles:\n  work:\n    *e:\n      A: 1\n"), &c)
	if err == nil || !strings.Contains(err.Error(), "enver can read") {
		t.Fatalf("want an unreadable-env error, got %v", err)
	}
}

func profileConfig(name string, env map[string]string, markedKey string) Config {
	p := Profile{Env: env}
	if markedKey != "" {
		p.Marked = map[string]bool{markedKey: true}
	}
	return Config{Profiles: map[string]Profile{name: p}}
}

func TestMergeLocalPlainKeyClearsInheritedMark(t *testing.T) {
	base := profileConfig("work", map[string]string{"TOKEN": "a"}, "TOKEN")
	local := profileConfig("work", map[string]string{"TOKEN": "b"}, "")
	got := Merge(base, local, LayerGlobal, LayerLocal).Profiles["work"]
	if got.Marked["TOKEN"] {
		t.Fatal("local unmarked override must clear the mark")
	}
	if got.Marked != nil {
		t.Fatalf("a merged profile left with no marks must hold nil Marked: %#v", got.Marked)
	}
}

func TestResolveCloserOverrideClearsMark(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"TOKEN": "a"}, Marked: map[string]bool{"TOKEN": true}},
		"work": {Extends: Extends{"base"}, Env: map[string]string{"TOKEN": "b"}},
	}}
	r, err := cfg.ResolveProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if r.Marked["TOKEN"] {
		t.Fatal("own unmarked entry must clear the inherited mark")
	}
	if r.Marked != nil {
		t.Fatalf("a resolution left with no marks must hold nil Marked: %#v", r.Marked)
	}
}

func TestResolveInheritsMarkWhenUntouched(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"TOKEN": "a"}, Marked: map[string]bool{"TOKEN": true}},
		"work": {Extends: Extends{"base"}},
	}}
	r, err := cfg.ResolveProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Marked["TOKEN"] {
		t.Fatal("an untouched inherited mark must survive")
	}
}

func TestUnsetFenceDropsMark(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"TOKEN": "a"}, Marked: map[string]bool{"TOKEN": true}},
		"work": {Extends: Extends{"base"}, Unset: Unsets{"TOKEN"}},
	}}
	r, err := cfg.ResolveProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if r.Marked["TOKEN"] {
		t.Fatal("a fenced key keeps no mark")
	}
}

func TestResolveCaseVariantClearsMarkOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("env names are case-sensitive elsewhere")
	}
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"Path": "a"}, Marked: map[string]bool{"Path": true}},
		"work": {Extends: Extends{"base"}, Env: map[string]string{"PATH": "b"}},
	}}
	r, err := cfg.ResolveProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if marked, _ := envname.Get(r.Marked, "PATH"); marked {
		t.Fatal("a case-variant override must clear the mark on Windows")
	}
}

func TestResolveSiblingParentUnmarkedOverrideClearsMark(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"a":    {Env: map[string]string{"TOKEN": "x"}, Marked: map[string]bool{"TOKEN": true}},
		"b":    {Env: map[string]string{"TOKEN": "y"}},
		"work": {Extends: Extends{"a", "b"}},
	}}
	r, err := cfg.ResolveProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if r.Env["TOKEN"] != "y" {
		t.Fatalf("later parent must win the value: %#v", r.Env)
	}
	if r.Marked != nil {
		t.Fatalf("a later parent's unmarked entry must clear the mark: %#v", r.Marked)
	}
}

func TestCarriedFenceDropsMark(t *testing.T) {
	global := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"TOKEN": "a"}, Marked: map[string]bool{"TOKEN": true}},
		"work": {Extends: Extends{"base"}, Unset: Unsets{"TOKEN"}},
	}}
	local := profileConfig("work", map[string]string{"OTHER": "b"}, "")
	r, err := Merge(global, local, LayerGlobal, LayerLocal).ResolveProfile("work")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Env["TOKEN"]; ok {
		t.Fatalf("the carried fence must strip the inherited value: %#v", r.Env)
	}
	if r.Marked["TOKEN"] {
		t.Fatalf("a fenced key keeps no mark: %#v", r.Marked)
	}
}

func TestMergeFencedOwnKeyDropsMark(t *testing.T) {
	base := Config{Profiles: map[string]Profile{
		"work": {Env: map[string]string{"TOKEN": "a"}, Marked: map[string]bool{"TOKEN": true}, Unset: Unsets{"TOKEN"}},
	}}
	local := profileConfig("work", map[string]string{"OTHER": "b"}, "")
	got := Merge(base, local, LayerGlobal, LayerLocal).Profiles["work"]
	if _, ok := got.Env["TOKEN"]; ok {
		t.Fatalf("the fence must strip the profile's own value: %#v", got.Env)
	}
	if got.Marked["TOKEN"] {
		t.Fatalf("a fenced key keeps no mark: %#v", got.Marked)
	}
}

func TestMarksForWritePrefersOwnEntry(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"TOKEN": "a"}, Marked: map[string]bool{"TOKEN": true}},
		"work": {Extends: Extends{"base"}, Env: map[string]string{"TOKEN": "b"}},
	}}
	if MarksForWrite(cfg, "work", []string{"TOKEN"})["TOKEN"] {
		t.Fatal("the profile's own unmarked entry must outrank the inherited mark")
	}
}

func TestMarksForWriteKeepsOwnMark(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"TOKEN": "a"}},
		"work": {Extends: Extends{"base"}, Env: map[string]string{"TOKEN": "b"}, Marked: map[string]bool{"TOKEN": true}},
	}}
	if !MarksForWrite(cfg, "work", []string{"TOKEN"})["TOKEN"] {
		t.Fatal("the profile's own marked entry must answer true, no ancestor mark needed")
	}
}

// TestMarksForWriteFoldsCaseOnWindows pins that a requested spelling reaches the
// entry resolution knows, the way the writer's own key lookup does.
func TestMarksForWriteFoldsCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("env names are case-sensitive elsewhere")
	}
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"TOKEN": "a"}},
		"work": {Extends: Extends{"base"}, Env: map[string]string{"Token": "b"}, Marked: map[string]bool{"Token": true}},
	}}
	if !MarksForWrite(cfg, "work", []string{"token"})["token"] {
		t.Fatal("a case-variant request must find the own marked entry on Windows")
	}
}

func TestMarksForWriteFallsBackToChain(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"TOKEN": "a"}, Marked: map[string]bool{"TOKEN": true}},
		"work": {Extends: Extends{"base"}},
	}}
	if !MarksForWrite(cfg, "work", []string{"TOKEN"})["TOKEN"] {
		t.Fatal("a key the profile does not define must take the chain's mark")
	}
}

// TestMarksForWriteAnswersEveryKey pins the map contract writers rely on: one
// non-nil entry per requested key, false for a key no chain marks, and a
// profile it cannot resolve (cycle, unknown name) answering false rather than
// failing.
func TestMarksForWriteAnswersEveryKey(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"TOKEN": "a"}, Marked: map[string]bool{"TOKEN": true}},
		"work": {Extends: Extends{"base"}},
		"loop": {Extends: Extends{"loop"}},
	}}
	got := MarksForWrite(cfg, "work", []string{"TOKEN", "PLAIN"})
	if !got["TOKEN"] || got["PLAIN"] {
		t.Fatalf("marks = %#v, want TOKEN marked and PLAIN not", got)
	}
	for _, name := range []string{"loop", "absent"} {
		if got := MarksForWrite(cfg, name, []string{"TOKEN"}); got == nil || got["TOKEN"] {
			t.Fatalf("MarksForWrite(%q) = %#v, want a non-nil false answer", name, got)
		}
	}
}

// useRemoteFixture arranges the layers a remote load test needs: a global
// config, one content file per name with a state sidecar pointing at the source
// remoteContentSource names (so the divergence check stays silent and only the
// behavior under test warns), and an optional .enver.yaml as the local layer.
// The returned loader reads the merged view with or without that local layer.
func useRemoteFixture(t *testing.T, global, local string, content map[string]string) func(useLocal bool) Config {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	remotes := t.TempDir()
	t.Cleanup(remotesDirIn(remotes))
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(xdg, "enver", "config.yaml"), global)
	for name, body := range content {
		write(RemoteContentPath(name), body)
		if err := WriteRemoteState(name, RemoteState{URL: remoteContentSource(name)}); err != nil {
			t.Fatal(err)
		}
	}
	if local != "" {
		proj := t.TempDir()
		write(filepath.Join(proj, LocalFilename), local)
		t.Cleanup(chdir(t, proj))
	}
	return func(useLocal bool) Config {
		t.Helper()
		cfg, err := LoadMerged("", useLocal)
		if err != nil {
			t.Fatalf("LoadMerged(useLocal=%v) err = %v, want no error", useLocal, err)
		}
		return cfg
	}
}

// remoteContentSource is the URL a fixture record points at, so the sidecar the
// fixture writes for that record's content matches it.
func remoteContentSource(name string) string {
	return "https://example.com/" + name + ".yaml"
}

func remoteRecordsYAML(names ...string) string {
	out := "remotes:\n"
	for _, name := range names {
		out += "  " + name + ":\n    url: " + remoteContentSource(name) + "\n"
	}
	return out
}

// prefixed collects the profile names a record's projection contributes, so a
// test can assert a record contributed nothing at all.
func prefixed(cfg Config, name string) []string {
	var out []string
	for _, n := range cfg.ProfileNames() {
		if strings.HasPrefix(n, name+"/") {
			out = append(out, n)
		}
	}
	return out
}

// The spec's case 1: a remote's own profile chain lands under its record's
// prefix, and the user's same-name profile folds per-key over it — the remote's
// endpoint values survive, the user's patch supplies the key.
func TestLoadMergedRemoteBase(t *testing.T) {
	const remoteBody = `default: ollama
profiles:
  ollama:
    env:
      OLLAMA_HOST: http://localhost:11434
      OLLAMA_MODEL_SERIES: v0.5
  qwen:
    extends: ollama
    env:
      MODEL: qwen2.5-coder:32b
`
	globalCase1 := remoteRecordsYAML("llm") + "profiles:\n  llm/ollama:\n    env:\n      OLLAMA_API_KEY: from-user\n"

	t.Run("case 1: the user patch folds over the projected remote", func(t *testing.T) {
		cfg := useRemoteFixture(t, globalCase1, "", map[string]string{"llm": remoteBody})(false)

		if got, want := prefixed(cfg, "llm"), []string{"llm/ollama", "llm/qwen"}; !slices.Equal(got, want) {
			t.Fatalf("projected profiles = %v, want %v", got, want)
		}
		// Prefixed profiles are addressed by their full name: no bare aliases.
		for _, bare := range []string{"ollama", "qwen"} {
			if _, ok := cfg.Profiles[bare]; ok {
				t.Fatalf("profile %q exists unprefixed; the prefix is the namespace", bare)
			}
		}
		// Which profile is default is the user's call, never the remote author's.
		if cfg.Default != "" {
			t.Fatalf("default = %q, want the remote's default dropped", cfg.Default)
		}
		if want := map[string]string{
			"OLLAMA_HOST":         "http://localhost:11434",
			"OLLAMA_MODEL_SERIES": "v0.5",
			"OLLAMA_API_KEY":      "from-user",
		}; !maps.Equal(cfg.Profiles["llm/ollama"].Env, want) {
			t.Fatalf("llm/ollama env = %v, want %v", cfg.Profiles["llm/ollama"].Env, want)
		}

		r, err := cfg.ResolveProfile("llm/qwen")
		if err != nil {
			t.Fatalf("ResolveProfile(llm/qwen) err = %v", err)
		}
		wantEnv := map[string]string{
			"OLLAMA_HOST":         "http://localhost:11434",
			"OLLAMA_MODEL_SERIES": "v0.5",
			"OLLAMA_API_KEY":      "from-user",
			"MODEL":               "qwen2.5-coder:32b",
		}
		if !maps.Equal(r.Env, wantEnv) {
			t.Fatalf("llm/qwen env = %v, want %v", r.Env, wantEnv)
		}
		if want := []string{"llm/qwen", "llm/ollama"}; !slices.Equal(r.Chain, want) {
			t.Fatalf("chain = %v, want %v", r.Chain, want)
		}
		if got := r.Sources["OLLAMA_HOST"]; got != (Source{Profile: "llm/ollama", Layer: "remote:llm"}) {
			t.Fatalf("OLLAMA_HOST source = %+v, want the projected remote", got)
		}
		if got := r.Sources["OLLAMA_API_KEY"]; got != (Source{Profile: "llm/ollama", Layer: LayerGlobal}) {
			t.Fatalf("OLLAMA_API_KEY source = %+v, want the user's global patch", got)
		}
		if got := cfg.ProfileLayer["llm/ollama"]; got != LayerGlobal {
			t.Fatalf("llm/ollama declaring layer = %q, want the patching layer global", got)
		}
		if got := cfg.ProfileLayer["llm/qwen"]; got != "remote:llm" {
			t.Fatalf("llm/qwen declaring layer = %q, want remote:llm", got)
		}
	})

	t.Run("a record with no content contributes nothing", func(t *testing.T) {
		global := remoteRecordsYAML("llm", "later") + "profiles:\n  user:\n    env:\n      K: v\n"
		cfg := useRemoteFixture(t, global, "", map[string]string{"llm": remoteBody})(false)

		if got := prefixed(cfg, "later"); len(got) != 0 {
			t.Fatalf("profiles = %v, want none for a record never pulled", got)
		}
		if _, ok := cfg.Remotes["later"]; !ok {
			t.Fatal("the unpulled record itself must stay declared")
		}
		if len(cfg.Warnings) != 0 {
			t.Fatalf("warnings = %q, want none: an unpulled record is not an error or a projection warning", cfg.Warnings)
		}
	})

	t.Run("remote content cannot register a fetch", func(t *testing.T) {
		content := "remotes:\n  injected:\n    url: https://attacker.example/evil.yaml\n" +
			"profiles:\n  base:\n    env:\n      K: v\n"
		cfg := useRemoteFixture(t, remoteRecordsYAML("llm"), "", map[string]string{"llm": content})(false)

		if len(cfg.Remotes) != 1 || cfg.Remotes["llm"].URL != remoteContentSource("llm") {
			t.Fatalf("merged remotes = %v, want only the user's record", cfg.Remotes)
		}
		if got, want := prefixed(cfg, "llm"), []string{"llm/base"}; !slices.Equal(got, want) {
			t.Fatalf("projected profiles = %v, want %v", got, want)
		}
	})

	t.Run("local beats global beats remote", func(t *testing.T) {
		global := remoteRecordsYAML("llm") +
			"profiles:\n  llm/base:\n    env:\n      K: global\n  plain:\n    env:\n      K: global\n"
		local := "profiles:\n  llm/base:\n    env:\n      K: local\n  plain:\n    env:\n      K: local\n"
		content := "profiles:\n  base:\n    env:\n      K: remote\n      REMOTE_ONLY: r\n"
		load := useRemoteFixture(t, global, local, map[string]string{"llm": content})

		for _, tc := range []struct {
			useLocal bool
			wantK    string
		}{{true, "local"}, {false, "global"}} {
			cfg := load(tc.useLocal)
			r, err := cfg.ResolveProfile("llm/base")
			if err != nil {
				t.Fatalf("ResolveProfile(llm/base) err = %v", err)
			}
			if r.Env["K"] != tc.wantK {
				t.Fatalf("useLocal=%v: K = %q, want %q", tc.useLocal, r.Env["K"], tc.wantK)
			}
			if r.Env["REMOTE_ONLY"] != "r" {
				t.Fatalf("useLocal=%v: REMOTE_ONLY = %q, want the remote's value to survive", tc.useLocal, r.Env["REMOTE_ONLY"])
			}
			plain, err := cfg.ResolveProfile("plain")
			if err != nil {
				t.Fatalf("ResolveProfile(plain) err = %v", err)
			}
			wantPlainK := LayerGlobal
			if tc.useLocal {
				wantPlainK = "local"
			}
			if plain.Env["K"] != wantPlainK {
				t.Fatalf("useLocal=%v: plain K = %q, want %q", tc.useLocal, plain.Env["K"], wantPlainK)
			}
			// A profile that never references remote content gains nothing from it.
			if _, ok := plain.Env["REMOTE_ONLY"]; ok {
				t.Fatalf("useLocal=%v: plain inherited a remote key: %v", tc.useLocal, plain.Env)
			}
		}
	})

	t.Run("a remote fence bites only remote-era values", func(t *testing.T) {
		// The remote's child fences K while its parent supplies one, and the user
		// layer redefines K on that parent. The fence is carried when the user
		// patches child, and it must stand down for the higher layer's value.
		content := "profiles:\n" +
			"  parent:\n    env:\n      K: remote\n" +
			"  child:\n    extends: parent\n    unset: [K]\n"
		global := remoteRecordsYAML("llm") + "profiles:\n" +
			"  llm/parent:\n    env:\n      K: user\n" +
			"  llm/child:\n    env:\n      PATCH: x\n"
		cfg := useRemoteFixture(t, global, "", map[string]string{"llm": content})(false)

		r, err := cfg.ResolveProfile("llm/child")
		if err != nil {
			t.Fatalf("ResolveProfile(llm/child) err = %v", err)
		}
		if r.Env["K"] != "user" {
			t.Fatalf("K = %q, want the user layer's value to survive the remote fence", r.Env["K"])
		}
		if _, tombstoned := r.Unsets["K"]; tombstoned {
			t.Fatalf("K reported as unset (%+v) although the user layer defines it", r.Unsets["K"])
		}
	})
}

// Content left behind for a record no layer declares can never load and no pull
// would refresh it: it contributes no profiles and says so.
func TestLoadMergedOrphanContent(t *testing.T) {
	orphan := "profiles:\n  base:\n    env:\n      K: stale\n"
	cfg := useRemoteFixture(t, "profiles:\n  user:\n    env:\n      K: v\n", "", map[string]string{"stale": orphan})(false)

	if got := prefixed(cfg, "stale"); len(got) != 0 {
		t.Fatalf("profiles = %v, want none from orphaned content", got)
	}
	if _, ok := cfg.Remotes["stale"]; ok {
		t.Fatal("orphaned content must not register its own record")
	}
	if len(cfg.Warnings) != 1 {
		t.Fatalf("warnings = %q, want exactly one orphan advisory", cfg.Warnings)
	}
	for _, want := range []string{"stale", "no record"} {
		if !strings.Contains(cfg.Warnings[0], want) {
			t.Fatalf("warning %q does not mention %q", cfg.Warnings[0], want)
		}
	}
}

// A pulled file that came back empty is a valid (if useless) config: no
// profiles, no error.
func TestLoadMergedEmptyContentFile(t *testing.T) {
	cfg := useRemoteFixture(t, remoteRecordsYAML("llm"), "", map[string]string{"llm": ""})(false)

	if len(cfg.Profiles) != 0 {
		t.Fatalf("profiles = %v, want none from an empty content file", cfg.ProfileNames())
	}
	if _, ok := cfg.Remotes["llm"]; !ok {
		t.Fatal("the record must stay declared when its content is empty")
	}
	if len(cfg.Warnings) != 0 {
		t.Fatalf("warnings = %q, want none", cfg.Warnings)
	}
}

// Two remotes at once: content composes across records through the passthrough
// extends rule, every entry keeps the remote that supplied it, and a record may
// be declared by the local layer alone. A same-projected-name collision between
// two records is unreachable while every record is prefixed — record names are
// single-level and distinct, so the prefixes are disjoint; the spec scopes that
// rule to no-prefix records, and the collision case belongs here when that flag
// lands.
func TestLoadMergedMultipleRemotes(t *testing.T) {
	content := map[string]string{
		"a": "profiles:\n  app:\n    extends: b/foo\n    env:\n      A_KEY: from-a\n",
		"b": "profiles:\n  foo:\n    env:\n      B_KEY: from-b\n",
	}
	global := remoteRecordsYAML("a") + "profiles:\n  user:\n    env:\n      K: global\n"
	local := remoteRecordsYAML("b")
	load := useRemoteFixture(t, global, local, content)

	cfg := load(true)
	if _, ok := cfg.Remotes["b"]; !ok {
		t.Fatalf("merged remotes = %v, want the record the local layer declares", cfg.Remotes)
	}
	if got, want := prefixed(cfg, "a"), []string{"a/app"}; !slices.Equal(got, want) {
		t.Fatalf("a's profiles = %v, want %v", got, want)
	}
	if got, want := prefixed(cfg, "b"), []string{"b/foo"}; !slices.Equal(got, want) {
		t.Fatalf("b's profiles = %v, want %v", got, want)
	}
	// The target already names the projected form, so it is not rewritten — it
	// binds remote b's profile and nothing else.
	wantExtends := Extends{"b/foo"}
	if !slices.Equal(cfg.Profiles["a/app"].Extends, wantExtends) {
		t.Fatalf("a/app extends = %v, want %v", cfg.Profiles["a/app"].Extends, wantExtends)
	}

	r, err := cfg.ResolveProfile("a/app")
	if err != nil {
		t.Fatalf("ResolveProfile(a/app) err = %v", err)
	}
	if want := map[string]string{"A_KEY": "from-a", "B_KEY": "from-b"}; !maps.Equal(r.Env, want) {
		t.Fatalf("a/app env = %v, want %v", r.Env, want)
	}
	if want := []string{"a/app", "b/foo"}; !slices.Equal(r.Chain, want) {
		t.Fatalf("chain = %v, want %v", r.Chain, want)
	}
	// Each remote keeps its own label through the multi-remote fold.
	if got := r.Sources["A_KEY"]; got != (Source{Profile: "a/app", Layer: "remote:a"}) {
		t.Fatalf("A_KEY source = %+v, want remote:a", got)
	}
	if got := r.Sources["B_KEY"]; got != (Source{Profile: "b/foo", Layer: "remote:b"}) {
		t.Fatalf("B_KEY source = %+v, want remote:b", got)
	}
	// The fold does not depend on map iteration order.
	if again := load(true); !reflect.DeepEqual(cfg, again) {
		t.Fatalf("two loads of one fixture differ:\n%+v\n%+v", cfg, again)
	}

	// Without the local layer the record is not in view, so its content is
	// unclaimed and the orphan advisory reports it.
	globalOnly := load(false)
	if got := prefixed(globalOnly, "b"); len(got) != 0 {
		t.Fatalf("profiles = %v, want none for a record only the local layer declares", got)
	}
	if len(globalOnly.Warnings) != 1 || !strings.Contains(globalOnly.Warnings[0], `"b"`) {
		t.Fatalf("warnings = %q, want exactly one advisory about b's unclaimed content", globalOnly.Warnings)
	}
}

// Content that is on disk but unreadable must not vanish from resolution in
// silence: its stat succeeds, so the divergence check has nothing to say about
// it, and the load raises its own advisory instead.
func TestLoadMergedUnreadableContentFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not deny reads on Windows")
	}
	load := useRemoteFixture(t, remoteRecordsYAML("llm"), "",
		map[string]string{"llm": "profiles:\n  base:\n    env:\n      K: v\n"})
	if err := os.Chmod(RemoteContentPath("llm"), 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(RemoteContentPath("llm")); err == nil {
		t.Skip("the filesystem granted the read despite mode 000 (running as root?)")
	}

	cfg := load(false)
	if len(cfg.Profiles) != 0 {
		t.Fatalf("profiles = %v, want none from content that cannot be read", cfg.ProfileNames())
	}
	if len(cfg.Warnings) != 1 {
		t.Fatalf("warnings = %q, want exactly one advisory about unreadable content", cfg.Warnings)
	}
	for _, want := range []string{"cannot be read", "enver pull llm"} {
		if !strings.Contains(cfg.Warnings[0], want) {
			t.Fatalf("warning %q does not mention %q", cfg.Warnings[0], want)
		}
	}
}
