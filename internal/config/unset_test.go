package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestResolveUnsetRemovesInheritedKey(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"anth": {Env: map[string]string{"API_KEY": "base", "MODEL": "m"}, Comments: map[string]string{"API_KEY": "hint"}},
		"bare": {Extends: Extends{"anth"}, Unset: Unsets{"API_KEY"}},
	}}
	r, err := cfg.ResolveProfile("bare")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Env["API_KEY"]; ok {
		t.Fatalf("unset key still in env: %v", r.Env)
	}
	if r.Env["MODEL"] != "m" {
		t.Fatalf("non-unset inherited key dropped: %v", r.Env)
	}
	if _, ok := r.Comments["API_KEY"]; ok {
		t.Fatalf("unset key's comment leaked: %v", r.Comments)
	}
}

func TestResolveUnsetCarriesThroughChain(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"root": {Env: map[string]string{"A": "1", "B": "1"}},
		"mid":  {Extends: Extends{"root"}, Unset: Unsets{"A"}},
		"leaf": {Extends: Extends{"mid"}, Env: map[string]string{"C": "3"}},
	}}
	r, err := cfg.ResolveProfile("leaf")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Env["A"]; ok {
		t.Fatalf("parent unset key A leaked into child env: %v", r.Env)
	}
	if r.Env["B"] != "1" || r.Env["C"] != "3" {
		t.Fatalf("env = %v, want B=1 C=3", r.Env)
	}
}

func TestResolveUnsetWinsOverOwnEnv(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"p": {Env: map[string]string{"A": "1"}, Unset: Unsets{"A"}},
	}}
	r, err := cfg.ResolveProfile("p")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Env["A"]; ok {
		t.Fatalf("unset did not win over own env: %v", r.Env)
	}
}

func TestUnsetYAMLScalarAndSequence(t *testing.T) {
	var cfg Config
	doc := "profiles:\n  a:\n    unset: API_KEY\n    env:\n      K: v\n  b:\n    unset: [A, B]\n    env:\n      K: v\n"
	if err := yaml.Unmarshal([]byte(doc), &cfg); err != nil {
		t.Fatal(err)
	}
	if !sliceEq(cfg.Profiles["a"].Unset, []string{"API_KEY"}) {
		t.Fatalf("scalar unset = %v, want [API_KEY]", cfg.Profiles["a"].Unset)
	}
	if !sliceEq(cfg.Profiles["b"].Unset, []string{"A", "B"}) {
		t.Fatalf("sequence unset = %v, want [A B]", cfg.Profiles["b"].Unset)
	}
}

func TestMergeUnsetsUnion(t *testing.T) {
	base := Config{Profiles: map[string]Profile{
		"p": {Unset: Unsets{"A"}, Env: map[string]string{"K": "base"}},
	}}
	over := Config{Profiles: map[string]Profile{
		"p": {Unset: Unsets{"B", "C"}, Env: map[string]string{"K": "over"}},
	}}
	u := Merge(base, over).Profiles["p"].Unset
	if !sliceEq(u, []string{"A", "B", "C"}) {
		t.Fatalf("merged unset = %v, want [A B C]", u)
	}
}

func TestResolveSourcesProvenance(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"anth":  {Env: map[string]string{"MODEL": "sonnet", "BASE": "g-url"}},
		"proxy": {Env: map[string]string{"BASE": "p-url"}},
		"dev":   {Extends: Extends{"anth", "proxy"}, Env: map[string]string{"API_KEY": "sk-dev"}},
	}}
	r, err := cfg.ResolveProfile("dev")
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Sources["MODEL"]; s != (Source{Profile: "anth", Layer: "global"}) {
		t.Errorf("MODEL source = %+v, want {anth global}", s)
	}
	if s := r.Sources["BASE"]; s.Profile != "proxy" {
		t.Errorf("BASE source = %+v, want proxy (later parent wins)", s)
	}
	if s := r.Sources["API_KEY"]; s.Profile != "dev" {
		t.Errorf("API_KEY source = %+v, want dev (own env wins)", s)
	}
}

func TestResolveSourcesMergedLayers(t *testing.T) {
	global := Config{Profiles: map[string]Profile{
		"anth": {Env: map[string]string{"MODEL": "sonnet", "BASE": "g-url"}},
	}}
	local := Config{Profiles: map[string]Profile{
		"anth": {Env: map[string]string{"BASE": "l-url"}},
		"dev":  {Extends: Extends{"anth"}, Env: map[string]string{"API_KEY": "sk-dev"}},
	}}
	cfg := Merge(global, local)
	r, err := cfg.ResolveProfile("dev")
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Sources["MODEL"]; s != (Source{Profile: "anth", Layer: "global"}) {
		t.Errorf("MODEL source = %+v, want {anth global} (defined only in global)", s)
	}
	if s := r.Sources["BASE"]; s != (Source{Profile: "anth", Layer: "local"}) {
		t.Errorf("BASE source = %+v, want {anth local} (overridden by local)", s)
	}
	if s := r.Sources["API_KEY"]; s != (Source{Profile: "dev", Layer: "local"}) {
		t.Errorf("API_KEY source = %+v, want {dev local} (local-only profile)", s)
	}
}

func TestValidateContradictoryUnset(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"bad":  {Env: map[string]string{"A": "1"}, Unset: Unsets{"A"}},
		"good": {Env: map[string]string{"A": "1"}, Unset: Unsets{"B"}},
	}}
	found := false
	for _, is := range Validate(cfg) {
		if is.Profile == "bad" && is.Kind == "contradictory-unset" && is.Severity == "warning" {
			found = true
		}
		if is.Profile == "good" {
			t.Error("good profile reported an issue")
		}
	}
	if !found {
		t.Fatal("contradictory unset not flagged")
	}
}

func TestUpsertPreservesUnsetOnDuplicate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("profiles:\n  src:\n    unset: [A, B]\n    env:\n      K: v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prof, _, _, ok, err := ReadProfile(path, "src")
	if err != nil || !ok {
		t.Fatalf("read: ok=%v err=%v", ok, err)
	}
	if err := UpsertProfile(path, "dst", prof, false, true); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "unset:") || !strings.Contains(s, "- A") || !strings.Contains(s, "- B") {
		t.Fatalf("unset lost on duplicate:\n%s", s)
	}
}

func TestResolveChildRedefinitionOverridesInheritedUnset(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"A": "base"}},
		"mid":  {Extends: Extends{"base"}, Unset: Unsets{"A"}},
		"leaf": {Extends: Extends{"mid"}, Env: map[string]string{"A": "child", "B": "2"}, Comments: map[string]string{"A": "hint"}},
	}}
	r, err := cfg.ResolveProfile("leaf")
	if err != nil {
		t.Fatal(err)
	}
	if r.Env["A"] != "child" {
		t.Fatalf("closer redefinition did not override the inherited unset: %v", r.Env)
	}
	if r.Env["B"] != "2" {
		t.Fatalf("env = %v, want A=child B=2", r.Env)
	}
	if r.Comments["A"] != "hint" {
		t.Fatalf("redefined key's comment lost: %v", r.Comments)
	}
	if s := r.Sources["A"]; s.Profile != "leaf" {
		t.Fatalf("redefined key's source = %+v, want leaf", s)
	}
}

func TestUnsetMappingYAMLIsError(t *testing.T) {
	for _, doc := range []string{
		"profiles:\n  a:\n    unset:\n      FOO: reason\n    env:\n      K: v\n",
		"profiles:\n  a:\n    extends:\n      anth: null\n    env:\n      K: v\n",
	} {
		var cfg Config
		if err := yaml.Unmarshal([]byte(doc), &cfg); err == nil {
			t.Fatalf("mapping node silently accepted:\n%s", doc)
		}
	}
}

func TestValidateUnsetOnlyProfileIsNotEmpty(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"bare": {Unset: Unsets{"ANTHROPIC_API_KEY"}},
	}}
	for _, is := range Validate(cfg) {
		if is.Kind == "empty" {
			t.Fatalf("unset-only profile flagged empty: %v", is)
		}
	}
}

func TestWriteProfileUnsetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := WriteProfile(path, "p", Profile{Unset: Unsets{"A", "B"}, Env: map[string]string{"K": "v"}}, false, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(got); !strings.Contains(s, "unset:") || !strings.Contains(s, "- A") || !strings.Contains(s, "- B") {
		t.Fatalf("unset not written:\n%s", s)
	}
	if err := WriteProfile(path, "p", Profile{Env: map[string]string{"K": "v"}}, false, false); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(got); strings.Contains(s, "unset") {
		t.Fatalf("empty Unsets did not clear the field:\n%s", s)
	}
}
