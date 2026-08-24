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
			"c": {Extends: Extends{"a"}, Env: map[string]string{"K5": "over"}},
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
	if !got.Profiles["c"].Extends.Has("a") {
		t.Fatalf("c.extends = %q, want a", got.Profiles["c"].Extends)
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
		if !sliceEq(got, c.want) {
			t.Fatalf("%s: mergeExtends(%v, %v) = %v, want %v", c.name, c.base, c.over, got, c.want)
		}
	}

	// A profile defined in both layers composes extends as [global…, local…].
	base := Config{Profiles: map[string]Profile{"p": {Extends: Extends{"g"}, Env: map[string]string{"K": "base"}}}}
	over := Config{Profiles: map[string]Profile{"p": {Extends: Extends{"l"}, Env: map[string]string{"K2": "over"}}}}
	merged := Merge(base, over)
	if got := merged.Profiles["p"].Extends; !sliceEq(got, Extends{"g", "l"}) {
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
	if got, want := r.Chain, []string{"leaf", "mid", "root"}; !sliceEq(got, want) {
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
	if err == nil {
		t.Fatal("cycle not detected")
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

func TestMaskValueURLSecrets(t *testing.T) {
	cases := []struct{ k, v string }{
		{"DATABASE_URL", "postgres://user:pass@db.internal:5432/app"},
		{"CONNECTION_STRING", "mysql://root:s3cr3t@host:3306/db"},
		{"REDIS_URL", "redis://:verysecret@cache:6379"},
		{"DATABASE_URL", "postgres://user:Abcdef/Xy@db.internal:5432/app"},
		{"GITHUB_URL", "https://ghp_xxx@github.com/org/repo"},
	}
	for _, c := range cases {
		if got := MaskValue(c.k, c.v); got == c.v {
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
		if got := MaskValue(c.k, c.v); got != c.v {
			t.Fatalf("MaskValue(%q) must not redact a URL without credentials, got %q", c.k, got)
		}
	}
}

func TestGlobalPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got, want := GlobalPath(""), filepath.Join("/xdg", "enver", "config.yaml"); got != want {
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

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a) && i < len(b); i++ {
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
	if !sliceEq(got, want) {
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

// TestResolveCommentsAcrossUsesReceiverChain verifies the method resolves
// comments along the receiver's chain (edit-aware), not the on-disk chain. This
// is what lets an in-progress edit see its own uncommitted extends change when
// seeding an override, while still spanning merged layers like dotenv.
func TestResolveCommentsAcrossUsesReceiverChain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	const yamlDoc = "profiles:\n" +
		"  base:\n" +
		"    env:\n" +
		"      # base cmt\n" +
		"      FOO: bv\n" +
		"  other:\n" +
		"    env:\n" +
		"      # other cmt\n" +
		"      FOO: ov\n" +
		"  dev:\n" +
		"    extends: base\n" +
		"    env:\n" +
		"      OWN: x\n"
	if err := os.WriteFile(path, []byte(yamlDoc), 0o644); err != nil {
		t.Fatal(err)
	}

	// Receiver simulates an in-progress edit: dev now extends other, not base.
	cfg := Config{Profiles: map[string]Profile{
		"base":  {Env: map[string]string{"FOO": "bv"}},
		"other": {Env: map[string]string{"FOO": "ov"}},
		"dev":   {Extends: Extends{"other"}, Env: map[string]string{"OWN": "x"}},
	}}
	got, err := cfg.ResolveCommentsAcross(path, false, "dev")
	if err != nil {
		t.Fatalf("ResolveCommentsAcross: %v", err)
	}
	if got["FOO"] != "other cmt" {
		t.Fatalf("FOO comment = %q, want %q (receiver chain dev→other)", got["FOO"], "other cmt")
	}

	// The free function, using the on-disk chain (dev→base), disagrees —
	// confirming the method is edit-aware, not a duplicate of the disk resolver.
	disk, err := ResolveCommentsMerged(path, false, "dev")
	if err != nil {
		t.Fatalf("ResolveCommentsMerged: %v", err)
	}
	if disk["FOO"] != "base cmt" {
		t.Fatalf("disk FOO comment = %q, want %q", disk["FOO"], "base cmt")
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
	if got, want := r.Chain, []string{"mix", "trait1", "base", "trait2"}; !sliceEq(got, want) {
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
	if got := cfg.Profiles["mix"].Extends; !sliceEq(got, []string{"a", "b"}) {
		t.Fatalf("list extends = %v, want [a b]", got)
	}
	// scalar form still works
	if err := os.WriteFile(path, []byte("profiles:\n  one:\n    extends: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ = load(path)
	if got := cfg.Profiles["one"].Extends; !sliceEq(got, []string{"a"}) {
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
	r, err := Merge(g, l).ResolveProfile("self")
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
	got := Merge(base, over).Profiles["p"]
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
