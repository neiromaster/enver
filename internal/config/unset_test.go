package config

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/neiromaster/enver/internal/envname"
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
	if !slices.Equal(cfg.Profiles["a"].Unset, []string{"API_KEY"}) {
		t.Fatalf("scalar unset = %v, want [API_KEY]", cfg.Profiles["a"].Unset)
	}
	if !slices.Equal(cfg.Profiles["b"].Unset, []string{"A", "B"}) {
		t.Fatalf("sequence unset = %v, want [A B]", cfg.Profiles["b"].Unset)
	}
}

// TestMergeCrossLayerDefineBeatsLayerUnset pins the layer rule: the global
// copy applies its own unset at its merge point, so the cwd copy defining the
// key afterwards wins instead of dying silently.
func TestMergeCrossLayerDefineBeatsLayerUnset(t *testing.T) {
	global := Config{Profiles: map[string]Profile{"dev": {Unset: Unsets{"TOKEN"}}}}
	local := Config{Profiles: map[string]Profile{"dev": {Env: map[string]string{"TOKEN": "v"}}}}
	r, err := Merge(global, local).ResolveProfile("dev")
	if err != nil {
		t.Fatal(err)
	}
	if r.Env["TOKEN"] != "v" {
		t.Fatalf("TOKEN = %q (%v), want v: the defining layer applies after the unsetting one", r.Env["TOKEN"], r.Env)
	}
}

func TestMergeConsumesInheritedUnsetsBeforeOverride(t *testing.T) {
	global := Config{Profiles: map[string]Profile{
		"dev": {Env: map[string]string{"MODEL": "m", "STALE": "s"}, Unset: Unsets{"STALE"}},
	}}
	local := Config{Profiles: map[string]Profile{
		"dev":  {Env: map[string]string{"TOKEN": "t"}},
		"leaf": {Extends: Extends{"dev"}},
	}}
	cfg := Merge(global, local)
	r, err := cfg.ResolveProfile("dev")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Env["STALE"]; ok {
		t.Fatalf("STALE survived: %v — the defining layer's own unset must apply to it", r.Env)
	}
	if r.Env["MODEL"] != "m" || r.Env["TOKEN"] != "t" {
		t.Fatalf("env = %v, want MODEL=m TOKEN=t", r.Env)
	}
	lp, err := cfg.ResolveProfile("leaf")
	if err != nil {
		t.Fatal(err)
	}
	if lp.Env["TOKEN"] != "t" {
		t.Fatalf("leaf env = %v: the layered dev contributions must carry down the chain", lp.Env)
	}
	if _, ok := lp.Env["STALE"]; ok {
		t.Fatalf("leaf env = %v: a consumed unset must not resurface downstream", lp.Env)
	}
}

// TestMergeUnsetListIsTheOverridingLayers replaces the old union contract:
// inherited unsets were applied during the fold, so the merged profile's list
// is the overriding layer's own.
func TestMergeUnsetListIsTheOverridingLayers(t *testing.T) {
	base := Config{Profiles: map[string]Profile{
		"p": {Unset: Unsets{"A"}, Env: map[string]string{"K": "base"}},
	}}
	overNoUnset := Config{Profiles: map[string]Profile{"p": {Env: map[string]string{"K": "over"}}}}
	if u := Merge(base, overNoUnset).Profiles["p"].Unset; len(u) != 0 {
		t.Fatalf("merged unset = %v, want none: the inherited list was consumed, not carried", u)
	}
	baseNoUnset := Config{Profiles: map[string]Profile{"p": {Env: map[string]string{"K": "base"}}}}
	over := Config{Profiles: map[string]Profile{"p": {Unset: Unsets{"B", "C"}}}}
	if u := Merge(baseNoUnset, over).Profiles["p"].Unset; !slices.Equal(u, []string{"B", "C"}) {
		t.Fatalf("merged unset = %v, want [B C]", u)
	}
}

// TestMergeCarriedChainFenceBeatsAncestorDefine pins the chain half of
// layer-scoped unsets: a fence whose key lives in an extends ancestor is
// carried to resolve time, so adding a local copy that never mentions the
// key cannot resurrect it.
func TestMergeCarriedChainFenceBeatsAncestorDefine(t *testing.T) {
	global := Config{Profiles: map[string]Profile{
		"anth": {Env: map[string]string{"ANTHROPIC_API_KEY": "k", "MODEL": "m"}},
		"bare": {Extends: Extends{"anth"}, Unset: Unsets{"ANTHROPIC_API_KEY"}},
	}}
	local := Config{Profiles: map[string]Profile{"bare": {Env: map[string]string{"MODEL": "lm"}}}}
	r, err := Merge(global, local).ResolveProfile("bare")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Env["ANTHROPIC_API_KEY"]; ok {
		t.Fatalf("fenced key resurrected: %v — a global chain fence must survive an unrelated local override", r.Env)
	}
	if r.Env["MODEL"] != "lm" {
		t.Fatalf("MODEL = %q (%v), want lm from the local copy", r.Env["MODEL"], r.Env)
	}
}

// TestMergeLocalRefillThroughAncestorBeatsGlobalChainFence pins the era gate:
// a carried fence strips what the earlier layer supplied, but a value the
// later layer explicitly puts into the ancestor is a closer mention and wins.
func TestMergeLocalRefillThroughAncestorBeatsGlobalChainFence(t *testing.T) {
	global := Config{Profiles: map[string]Profile{
		"anth": {Env: map[string]string{"API_KEY": "g"}},
		"bare": {Extends: Extends{"anth"}, Unset: Unsets{"API_KEY"}},
	}}
	local := Config{Profiles: map[string]Profile{"anth": {Env: map[string]string{"API_KEY": "l"}}}}
	r, err := Merge(global, local).ResolveProfile("bare")
	if err != nil {
		t.Fatal(err)
	}
	if r.Env["API_KEY"] != "l" {
		t.Fatalf("API_KEY = %q (%v), want l — a later-era mention outranks an earlier-era fence", r.Env["API_KEY"], r.Env)
	}
	if s := r.Sources["API_KEY"]; s.Layer != LayerLocal {
		t.Fatalf("API_KEY source = %+v, want layer local", s)
	}
}

// TestMergeSelfFenceStaysStrippedAcrossLayers pins the self-fence fate: a
// fence that consumed its own entry must still ride against extends
// ancestors, or the presence of an unrelated local file alone would flip a
// stripped key back on; a same-name local refill still wins at its turn.
func TestMergeSelfFenceStaysStrippedAcrossLayers(t *testing.T) {
	selfFenceGlobal := func() Config {
		return Config{Profiles: map[string]Profile{
			"anth": {Env: map[string]string{"K": "g"}},
			"bare": {Extends: Extends{"anth"}, Env: map[string]string{"K": "s"}, Unset: Unsets{"K"}},
		}}
	}
	base, err := selfFenceGlobal().ResolveProfile("bare")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := base.Env["K"]; ok {
		t.Fatalf("unmerged global leaked K: %v", base.Env)
	}
	unrelated := Config{Profiles: map[string]Profile{"foo": {Env: map[string]string{"X": "1"}}}}
	merged, err := Merge(selfFenceGlobal(), unrelated).ResolveProfile("bare")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := merged.Env["K"]; ok {
		t.Fatalf("K = %q (%v): an unrelated local file must not undo the global fence", merged.Env["K"], merged.Env)
	}
	refill := Config{Profiles: map[string]Profile{"bare": {Env: map[string]string{"K": "l"}}}}
	won, err := Merge(selfFenceGlobal(), refill).ResolveProfile("bare")
	if err != nil {
		t.Fatal(err)
	}
	if won.Env["K"] != "l" {
		t.Fatalf("K = %q (%v), want l — the refill layer's turn comes last", won.Env["K"], won.Env)
	}
}

// TestResolveSiblingParentUnsetStaysInItsOwnBranch pins multi-parent
// composition: an unset removes what came up its OWN branch (b alone is
// clean), while a sibling parent supplies fresh values into the child
// regardless of that unset — closest mention at equal distance is the define.
func TestResolveSiblingParentUnsetStaysInItsOwnBranch(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"a": {Env: map[string]string{"K": "v"}},
		"b": {Extends: Extends{"a"}, Unset: Unsets{"K"}},
	}}
	solo, err := cfg.ResolveProfile("b")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := solo.Env["K"]; ok {
		t.Fatalf("b alone = %v, want K stripped along its own branch", solo.Env)
	}
	for _, parents := range []Extends{{"a", "b"}, {"b", "a"}} {
		child := Config{Profiles: map[string]Profile{"c": {Extends: parents}}}
		for name, p := range cfg.Profiles {
			child.Profiles[name] = p
		}
		r, err := child.ResolveProfile("c")
		if err != nil {
			t.Fatal(err)
		}
		if r.Env["K"] != "v" {
			t.Fatalf("extends %v: K = %q (%v), want v — a sibling parent defines afresh", parents, r.Env["K"], r.Env)
		}
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
	prof, _, ok, err := ReadProfile(path, "src")
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

// TestUnsetCoversBothSpellings pins the all-variants deletion: with
// both spellings authored (the shape that regressed under first-match
// removal), an unset of either spelling must leave nothing behind on
// Windows, while POSIX keeps the unpicked spelling a distinct variable.
func TestUnsetCoversBothSpellings(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{"p": {
		Env:   map[string]string{"PATH": "/bin", "Path": "/win"},
		Unset: Unsets{"Path"},
	}}}
	r, err := cfg.ResolveProfile("p")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if len(r.Env) != 0 {
			t.Fatalf("windows env = %v, want empty — deleting one spelling must take every case-variant", r.Env)
		}
		return
	}
	if r.Env["PATH"] != "/bin" {
		t.Fatalf("posix env = %v, want PATH intact (distinct variable)", r.Env)
	}
	if _, ok := r.Env["Path"]; ok {
		t.Fatalf("posix env = %v, want Path deleted by its own unset", r.Env)
	}
}

func TestUnmarshalUnsetDeduplicatesRepeats(t *testing.T) {
	var p Profile
	src := `
env:
  A: "1"
unset: [K2, K1, K2]
`
	if err := yaml.Unmarshal([]byte(src), &p); err != nil {
		t.Fatal(err)
	}
	want := Unsets{"K2", "K1"}
	if !slices.Equal(p.Unset, want) {
		t.Fatalf("unset after load = %v, want %v (first occurrence kept, repeats dropped)", p.Unset, want)
	}
}

func TestResolveUnsetsReportOwnUnset(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"anth": {Env: map[string]string{"API_KEY": "base", "MODEL": "m"}},
		"bare": {Extends: Extends{"anth"}, Unset: Unsets{"API_KEY"}},
	}}
	r, err := cfg.ResolveProfile("bare")
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Unsets["API_KEY"]; s != (Source{Profile: "bare", Layer: LayerGlobal}) {
		t.Fatalf("API_KEY unset source = %+v, want {bare global}", s)
	}
	if envname.Has(r.Unsets, "MODEL") {
		t.Fatalf("MODEL reported unset: %v", r.Unsets)
	}
}

func TestResolveUnsetsDeclaredRidesWithoutVictim(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{"p": {Unset: Unsets{"GHOST"}}}}
	r, err := cfg.ResolveProfile("p")
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Unsets["GHOST"]; s != (Source{Profile: "p", Layer: LayerGlobal}) {
		t.Fatalf("GHOST unset source = %+v, want {p global} — declared intent rides without a victim", s)
	}
}

func TestResolveUnsetsCloserRedefinitionClears(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"base": {Env: map[string]string{"A": "base"}},
		"mid":  {Extends: Extends{"base"}, Unset: Unsets{"A"}},
		"leaf": {Extends: Extends{"mid"}, Env: map[string]string{"A": "child"}},
	}}
	r, err := cfg.ResolveProfile("leaf")
	if err != nil {
		t.Fatal(err)
	}
	if envname.Has(r.Unsets, "A") {
		t.Fatalf("A reported unset after closer redefinition: %v", r.Unsets)
	}
}

func TestResolveUnsetsSiblingDefineLeavesNoTombstone(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"a": {Env: map[string]string{"K": "v"}},
		"b": {Extends: Extends{"a"}, Unset: Unsets{"K"}},
	}}
	solo, err := cfg.ResolveProfile("b")
	if err != nil {
		t.Fatal(err)
	}
	if s := solo.Unsets["K"]; s != (Source{Profile: "b", Layer: LayerGlobal}) {
		t.Fatalf("solo b Unsets[K] = %+v, want {b global}", s)
	}
	for _, parents := range []Extends{{"a", "b"}, {"b", "a"}} {
		child := Config{Profiles: map[string]Profile{"c": {Extends: parents}}}
		for name, p := range cfg.Profiles {
			child.Profiles[name] = p
		}
		r, err := child.ResolveProfile("c")
		if err != nil {
			t.Fatal(err)
		}
		if envname.Has(r.Unsets, "K") {
			t.Fatalf("extends %v: K reported unset while defined: %v", parents, r.Unsets)
		}
	}
}

func TestResolveUnsetsAncestorAttribution(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"root": {Env: map[string]string{"A": "1", "B": "1"}},
		"mid":  {Extends: Extends{"root"}, Unset: Unsets{"A"}},
		"leaf": {Extends: Extends{"mid"}},
	}}
	r, err := cfg.ResolveProfile("leaf")
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Unsets["A"]; s != (Source{Profile: "mid", Layer: LayerGlobal}) {
		t.Fatalf("A unset source = %+v, want {mid global} — the unsetting ancestor is named", s)
	}
}

func TestResolveUnsetsCarriedFenceReportsAndSilences(t *testing.T) {
	global := Config{Profiles: map[string]Profile{
		"anth": {Env: map[string]string{"ANTHROPIC_API_KEY": "k", "MODEL": "m"}},
		"bare": {Extends: Extends{"anth"}, Unset: Unsets{"ANTHROPIC_API_KEY"}},
	}}
	stripped, err := Merge(global, Config{Profiles: map[string]Profile{
		"bare": {Env: map[string]string{"MODEL": "lm"}},
	}}).ResolveProfile("bare")
	if err != nil {
		t.Fatal(err)
	}
	if s := stripped.Unsets["ANTHROPIC_API_KEY"]; s != (Source{Profile: "bare", Layer: LayerGlobal}) {
		t.Fatalf("carried strip source = %+v, want {bare global}", s)
	}
	silenced, err := Merge(global, Config{Profiles: map[string]Profile{
		"anth": {Env: map[string]string{"ANTHROPIC_API_KEY": "l"}},
	}}).ResolveProfile("bare")
	if err != nil {
		t.Fatal(err)
	}
	if envname.Has(silenced.Unsets, "ANTHROPIC_API_KEY") {
		t.Fatalf("later-era refill reported unset: %v", silenced.Unsets)
	}
}

func TestResolveUnsetsLayerAttribution(t *testing.T) {
	global := Config{Profiles: map[string]Profile{"dev": {Unset: Unsets{"A"}}}}
	local := Config{Profiles: map[string]Profile{"dev": {Unset: Unsets{"B"}}}}
	r, err := Merge(global, local).ResolveProfile("dev")
	if err != nil {
		t.Fatal(err)
	}
	if s := r.Unsets["A"]; s != (Source{Profile: "dev", Layer: LayerGlobal}) {
		t.Fatalf("A unset source = %+v, want {dev global}", s)
	}
	if s := r.Unsets["B"]; s != (Source{Profile: "dev", Layer: LayerLocal}) {
		t.Fatalf("B unset source = %+v, want {dev local}", s)
	}
}

// TestMergeChainedCarriedFenceAttribution pins fence layer stamping across
// chained merges: a fence the middle layer declared moves to Carried with
// that layer recorded, so resolution attributes the tombstone to local —
// not to the single-merge global default.
func TestMergeChainedCarriedFenceAttribution(t *testing.T) {
	global := Config{Profiles: map[string]Profile{
		"anth": {Env: map[string]string{"KEY": "g", "MODEL": "m"}},
		"bare": {Extends: Extends{"anth"}, Unset: Unsets{"KEY"}},
	}}
	mid := Config{Profiles: map[string]Profile{
		"bare": {Extends: Extends{"anth"}, Unset: Unsets{"KEY"}, Env: map[string]string{"MODEL": "l1"}},
	}}
	tail := Config{Profiles: map[string]Profile{
		"bare": {Env: map[string]string{"OTHER": "l2"}},
	}}
	r, err := Merge(Merge(global, mid), tail).ResolveProfile("bare")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Env["KEY"]; ok {
		t.Fatalf("fenced key survived the chained merge: %v", r.Env)
	}
	if s := r.Unsets["KEY"]; s != (Source{Profile: "bare", Layer: LayerLocal}) {
		t.Fatalf("KEY unset source = %+v, want {bare local} — the middle layer declared the fence", s)
	}
}

// TestResolveSiblingOrdersKeepNoTombstoneWithLiveKey pins the final sweep as
// the invariant's single enforcement point, in both sibling orders: [define,
// unset] lands the tombstone with the victim already resident, [unset,
// define] lands the victim after the tombstone. Either must end live without
// a tombstone, so weakening the sweep fails a test instead of regressing.
func TestResolveSiblingOrdersKeepNoTombstoneWithLiveKey(t *testing.T) {
	base := Config{Profiles: map[string]Profile{
		"a": {Env: map[string]string{"K": "v"}},
		"b": {Extends: Extends{"a"}, Unset: Unsets{"K"}},
	}}
	for _, parents := range []Extends{{"a", "b"}, {"b", "a"}} {
		cfg := Config{Profiles: map[string]Profile{"c": {Extends: parents}}}
		for name, p := range base.Profiles {
			cfg.Profiles[name] = p
		}
		r, err := cfg.ResolveProfile("c")
		if err != nil {
			t.Fatal(err)
		}
		if r.Env["K"] != "v" {
			t.Fatalf("extends %v: K = %q (%v), want v — the sibling define wins", parents, r.Env["K"], r.Env)
		}
		if envname.Has(r.Unsets, "K") {
			t.Fatalf("extends %v: tombstone on a live key: %v", parents, r.Unsets)
		}
	}
}
