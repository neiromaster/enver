package config

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestRemotesRecordMerge(t *testing.T) {
	base := Config{Remotes: map[string]RemoteRecord{
		"llm":    {URL: "github:someone/llm-profiles", Ref: "v1", NoPrefix: true},
		"legacy": {URL: "https://example.com/old.yaml"},
	}}
	over := Config{Remotes: map[string]RemoteRecord{
		"llm": {URL: "https://example.com/llm.yaml", Ref: "v2"},
	}}
	got := Merge(base, over, LayerGlobal, LayerLocal)
	if r := got.Remotes["llm"]; r != (RemoteRecord{URL: "https://example.com/llm.yaml", Ref: "v2"}) {
		t.Fatalf("merged llm = %+v, want the local record whole", r)
	}
	if r := got.Remotes["legacy"]; r != (RemoteRecord{URL: "https://example.com/old.yaml"}) {
		t.Fatalf("merged legacy = %+v, want the global-only record kept", r)
	}
	if base.Remotes["llm"].Ref != "v1" || !base.Remotes["llm"].NoPrefix {
		t.Fatalf("base remotes mutated: %+v", base.Remotes)
	}
	got.Remotes["legacy"] = RemoteRecord{URL: "https://example.com/new.yaml"}
	if base.Remotes["legacy"].URL != "https://example.com/old.yaml" {
		t.Fatal("merged remotes map aliases the base map")
	}

	// The record is the unit: an overriding record with no ref clears the
	// inherited ref rather than inheriting it, and the pull mode with it.
	partial := Merge(base, Config{Remotes: map[string]RemoteRecord{
		"llm": {URL: "https://example.com/llm.yaml"},
	}}, LayerGlobal, LayerLocal)
	if r := partial.Remotes["llm"]; r != (RemoteRecord{URL: "https://example.com/llm.yaml"}) {
		t.Fatalf("merged llm = %+v, want no inherited ref or prefix mode", r)
	}

	if r := Merge(Config{}, Config{}, LayerGlobal, LayerLocal).Remotes; r != nil {
		t.Fatalf("remotes = %v, want nil when no layer declares any", r)
	}
}

func TestRemotesYAMLParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "remotes:\n" +
		"  llm:\n" +
		"    url: github:someone/llm-profiles\n" +
		"    ref: v2\n" +
		"  bare:\n" +
		"    url: https://example.com/team.yaml\n" +
		"    no_prefix: true\n" +
		"  plain:\n" +
		"    url: https://example.com/profiles.yaml\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if r := cfg.Remotes["llm"]; r != (RemoteRecord{URL: "github:someone/llm-profiles", Ref: "v2"}) {
		t.Fatalf("llm = %+v, want url and ref", r)
	}
	if r := cfg.Remotes["bare"]; r != (RemoteRecord{URL: "https://example.com/team.yaml", NoPrefix: true}) {
		t.Fatalf("bare = %+v, want the pull mode persisted", r)
	}
	if r := cfg.Remotes["plain"]; r != (RemoteRecord{URL: "https://example.com/profiles.yaml"}) {
		t.Fatalf("plain = %+v, want url with an empty ref", r)
	}
}

func TestRemotesInvalidRecordName(t *testing.T) {
	for _, name := range []string{"llm", "llm-2", "LLM_1", "0"} {
		if !ValidRecordName(name) {
			t.Errorf("ValidRecordName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "a/b", "/x", "llm/", "a b", "-x", "llm.profiles"} {
		if ValidRecordName(name) {
			t.Errorf("ValidRecordName(%q) = true, want false", name)
		}
	}

	path := filepath.Join(t.TempDir(), "config.yaml")
	body := "remotes:\n  a/b:\n    url: https://example.com/x.yaml\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("load must reject a record name the prefix rule forbids")
	}
	if !strings.Contains(err.Error(), `"a/b"`) {
		t.Fatalf("err = %v, want it to name the record", err)
	}
}

// remotesDirIn points the remotes directory at dir for one test and returns the
// restore func.
func remotesDirIn(dir string) func() {
	prev := RemotesDir
	RemotesDir = func() string { return dir }
	return func() { RemotesDir = prev }
}

func TestRemoteStateRoundtrip(t *testing.T) {
	dir := t.TempDir()
	defer remotesDirIn(dir)()

	if _, ok := ReadRemoteState("llm"); ok {
		t.Fatal("ReadRemoteState reported state for a remote never pulled")
	}

	st := RemoteState{
		URL:      "https://raw.githubusercontent.com/me/llm/v2/enver.yaml",
		Ref:      "v2",
		Commit:   "9f2c1ab",
		PulledAt: time.Date(2026, 11, 15, 10, 30, 0, 0, time.UTC),
	}
	if err := WriteRemoteState("llm", st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(RemoteStatePath("llm")); err != nil {
		t.Fatalf("sidecar not written next to the content: %v", err)
	}
	got, ok := ReadRemoteState("llm")
	if !ok {
		t.Fatal("ReadRemoteState reported no state after a write")
	}
	if got.URL != st.URL || got.Ref != st.Ref || got.Commit != st.Commit || !got.PulledAt.Equal(st.PulledAt) {
		t.Fatalf("state = %+v, want %+v", got, st)
	}
}

// A sidecar that parses but names no source is not state: trusting it would
// report content as fetched from nowhere.
func TestRemoteStateWithoutSourceIsNotState(t *testing.T) {
	cases := []struct{ name, body string }{
		{"empty file", ""},
		{"empty mapping", "{}\n"},
		{"ref only", "ref: v2\n"},
		{"empty url", "url: \"\"\nref: v2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			defer remotesDirIn(dir)()
			if err := os.WriteFile(RemoteStatePath("llm"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, ok := ReadRemoteState("llm"); ok {
				t.Fatalf("ReadRemoteState trusted a sidecar holding %q", tc.body)
			}
		})
	}
}

// The exported content and state writers are all a pull needs, so internal/app
// never re-derives the file names or the atomic-write idiom.
func TestRemoteContentWriteAndRemoval(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "remotes")
	defer remotesDirIn(dir)()

	content := "profiles:\n  base:\n    env:\n      K: pulled\n"
	if err := WriteRemoteContent("llm", []byte(content)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "llm.yaml"))
	if err != nil {
		t.Fatalf("content not written where the remotes dir promises it: %v", err)
	}
	if string(got) != content {
		t.Fatalf("content = %q, want %q", got, content)
	}

	st := RemoteState{URL: "https://example.com/llm.yaml", Ref: "v2"}
	if err := WriteRemoteState("llm", st); err != nil {
		t.Fatal(err)
	}

	if err := RemoveRemoteContent("llm"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "llm.yaml")); !os.IsNotExist(err) {
		t.Fatalf("content survived removal: %v", err)
	}
	if err := RemoveRemoteContent("llm"); err != nil {
		t.Fatalf("removing absent content must be done, not an error: %v", err)
	}

	if err := RemoveRemoteState("llm"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadRemoteState("llm"); ok {
		t.Fatal("state survived removal")
	}
	if err := RemoveRemoteState("llm"); err != nil {
		t.Fatalf("removing absent state must be done, not an error: %v", err)
	}

	for _, name := range []string{"", "../escape"} {
		if err := WriteRemoteContent(name, nil); err == nil {
			t.Errorf("WriteRemoteContent(%q) accepted an unusable record name", name)
		}
		if err := RemoveRemoteContent(name); err == nil {
			t.Errorf("RemoveRemoteContent(%q) accepted an unusable record name", name)
		}
		if err := RemoveRemoteState(name); err == nil {
			t.Errorf("RemoveRemoteState(%q) accepted an unusable record name", name)
		}
	}

	if err := RemoveRemoteContent("a/b"); err != nil {
		t.Errorf("RemoveRemoteContent(a/b) on absent content = %v, want done", err)
	}
}

func TestValidRecordPath(t *testing.T) {
	for _, path := range []string{"ollama", "ollama/claude", "a/b/c"} {
		if !ValidRecordPath(path) {
			t.Errorf("ValidRecordPath(%q) = false, want true", path)
		}
	}
	for _, path := range []string{"", "/x", "x/", "a//b", "a/.", "..", "a/../b", "a/b.profiles"} {
		if ValidRecordPath(path) {
			t.Errorf("ValidRecordPath(%q) = true, want false", path)
		}
	}
}

func TestRemoteContentNestedRoundtrip(t *testing.T) {
	dir := t.TempDir()
	defer remotesDirIn(dir)()

	content := "profiles:\n  base:\n    env:\n      K: pulled\n"
	if err := WriteRemoteContent("ollama/claude", []byte(content)); err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(dir, "ollama", "claude.yaml")
	if RemoteContentPath("ollama/claude") != wantPath {
		t.Fatalf("RemoteContentPath = %q, want %q", RemoteContentPath("ollama/claude"), wantPath)
	}
	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("content not written where the path function promises it: %v", err)
	}
	if string(got) != content {
		t.Fatalf("content = %q, want %q", got, content)
	}

	st := RemoteState{URL: "https://example.com/claude.yaml", Ref: "v2", Commit: "9f2c1ab"}
	if err := WriteRemoteState("ollama/claude", st); err != nil {
		t.Fatal(err)
	}
	gotState, ok := ReadRemoteState("ollama/claude")
	if !ok {
		t.Fatal("ReadRemoteState reported no state after a write")
	}
	if gotState.URL != st.URL || gotState.Ref != st.Ref || gotState.Commit != st.Commit {
		t.Fatalf("state = %+v, want %+v", gotState, st)
	}

	for _, name := range []string{"a//b", ""} {
		err := WriteRemoteState(name, st)
		if err == nil {
			t.Errorf("WriteRemoteState(%q) accepted an unusable record path", name)
			continue
		}
		if !strings.Contains(err.Error(), "not a valid record path") {
			t.Errorf("WriteRemoteState(%q) err = %v, want it to name the record-path rule", name, err)
		}
	}
}

func TestRemoveRemoteContentRemovesSubtree(t *testing.T) {
	dir := t.TempDir()
	defer remotesDirIn(dir)()

	if err := WriteRemoteContent("ollama", []byte("profiles:\n  base:\n    env:\n      K: flat\n")); err != nil {
		t.Fatal(err)
	}
	if err := WriteRemoteContent("ollama/claude", []byte("profiles:\n  base:\n    env:\n      K: nested\n")); err != nil {
		t.Fatal(err)
	}
	if err := WriteRemoteState("ollama", RemoteState{URL: "https://example.com/ollama.yaml"}); err != nil {
		t.Fatal(err)
	}

	if err := RemoveRemoteContent("ollama"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(dir, "ollama.yaml"),
		filepath.Join(dir, "ollama", "claude.yaml"),
		filepath.Join(dir, "ollama"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived RemoveRemoteContent: %v", path, err)
		}
	}

	if err := RemoveRemoteState("ollama"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ollama.meta")); !os.IsNotExist(err) {
		t.Errorf(".meta survived RemoveRemoteState: %v", err)
	}
}

// A record name that escapes the remotes directory never reaches the filesystem:
// the writers validate it, and a reader facing such a name reports no state.
func TestRemoteStateRefusesUnsafeName(t *testing.T) {
	dir := t.TempDir()
	defer remotesDirIn(dir)()

	if err := WriteRemoteState("../escape", RemoteState{URL: "https://example.com/x.yaml"}); err == nil {
		t.Fatal("WriteRemoteState accepted a name outside the record-name rule")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("remotes dir holds %d entries after a refused write, want none", len(entries))
	}
	if _, ok := ReadRemoteState("../escape"); ok {
		t.Fatal("ReadRemoteState accepted a name outside the record-name rule")
	}
}

func TestDivergenceWarning(t *testing.T) {
	tests := []struct {
		name    string
		record  string
		local   string // .enver.yaml in cwd; empty loads the global layer only
		content bool
		state   *RemoteState
		want    []string // substrings the single warning must carry; nil wants none
		notWant []string // substrings the warning must not carry
	}{
		{
			name:    "record re-pointed at another source",
			record:  "remotes:\n  llm:\n    url: https://example.com/b.yaml\n    ref: v3\n",
			content: true,
			state:   &RemoteState{URL: "https://example.com/a.yaml", Ref: "v2"},
			want:    []string{"llm", "https://example.com/a.yaml", "v2", "https://example.com/b.yaml", "v3", "enver pull llm"},
		},
		{
			name:    "unpinned record reports the commit its content came from",
			record:  "remotes:\n  llm:\n    url: https://example.com/b.yaml\n",
			content: true,
			state:   &RemoteState{URL: "https://example.com/a.yaml", Commit: "9f2c1ab"},
			want:    []string{"https://example.com/a.yaml", "https://example.com/b.yaml", "pins no ref", "9f2c1ab", "enver pull llm"},
		},
		{
			name:    "pinned record needs no commit in the divergence warning",
			record:  "remotes:\n  llm:\n    url: https://example.com/b.yaml\n    ref: v2\n",
			content: true,
			state:   &RemoteState{URL: "https://example.com/a.yaml", Ref: "v1", Commit: "9f2c1ab"},
			want:    []string{"https://example.com/a.yaml", "https://example.com/b.yaml", "enver pull llm"},
			notWant: []string{"9f2c1ab"},
		},
		{
			name:    "github ref bumped in the record",
			record:  "remotes:\n  llm:\n    url: github:me/llm@v3\n",
			content: true,
			state:   &RemoteState{URL: "https://raw.githubusercontent.com/me/llm/v2/enver.yaml", Ref: "v2"},
			want:    []string{"https://raw.githubusercontent.com/me/llm/v2/enver.yaml", "https://raw.githubusercontent.com/me/llm/v3/enver.yaml", "enver pull llm"},
		},
		{
			name:    "content fetched for the source the record still points at",
			record:  "remotes:\n  llm:\n    url: github:me/llm@v2\n",
			content: true,
			state:   &RemoteState{URL: "https://raw.githubusercontent.com/me/llm/v2/enver.yaml", Ref: "v2"},
		},
		{
			name:    "plain https ref label is compared even though it never enters the URL",
			record:  "remotes:\n  llm:\n    url: https://example.com/c.yaml\n    ref: v3\n",
			content: true,
			state:   &RemoteState{URL: "https://example.com/c.yaml", Ref: "v2"},
			want:    []string{"https://example.com/c.yaml", "v2", "v3", "enver pull llm"},
		},
		{
			name:    "content with no pull state to compare asks for a re-pull",
			record:  "remotes:\n  llm:\n    url: https://example.com/c.yaml\n",
			content: true,
			want:    []string{"llm", "https://example.com/c.yaml", "enver pull llm"},
		},
		{
			name:    "sidecar without a source URL counts as no pull state",
			record:  "remotes:\n  llm:\n    url: https://example.com/c.yaml\n",
			content: true,
			state:   &RemoteState{},
			want:    []string{"no pull state to compare", "https://example.com/c.yaml", "enver pull llm"},
		},
		{
			name:   "state without content has nothing to disagree with",
			record: "remotes:\n  llm:\n    url: https://example.com/c.yaml\n",
			state:  &RemoteState{URL: "https://example.com/other.yaml", Ref: "v2"},
		},
		{
			name:    "record the resolver cannot read still gets a warning",
			record:  "remotes:\n  llm:\n    url: gitlab:me/llm\n",
			content: true,
			state:   &RemoteState{URL: "https://example.com/a.yaml", Ref: "v2"},
			want:    []string{"llm", "gitlab:me/llm", "enver pull llm"},
		},
		{
			name:    "the merged record is what the sidecar is compared against",
			record:  "remotes:\n  llm:\n    url: https://example.com/a.yaml\n    ref: v2\n",
			local:   "remotes:\n  llm:\n    url: https://example.com/b.yaml\n    ref: v3\n",
			content: true,
			state:   &RemoteState{URL: "https://example.com/a.yaml", Ref: "v2"},
			want:    []string{"https://example.com/a.yaml", "https://example.com/b.yaml", "v3", "enver pull llm"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			xdg := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", xdg)
			remotes := t.TempDir()
			defer remotesDirIn(remotes)()

			if err := os.MkdirAll(filepath.Join(xdg, "enver"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(xdg, "enver", "config.yaml"), []byte(tc.record), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.content {
				body := "profiles:\n  base:\n    env:\n      K: pulled\n"
				if err := os.WriteFile(filepath.Join(remotes, "llm.yaml"), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.state != nil {
				if err := WriteRemoteState("llm", *tc.state); err != nil {
					t.Fatal(err)
				}
			}
			useLocal := tc.local != ""
			if useLocal {
				proj := t.TempDir()
				if err := os.WriteFile(filepath.Join(proj, LocalFilename), []byte(tc.local), 0o644); err != nil {
					t.Fatal(err)
				}
				defer chdir(t, proj)()
			}

			cfg, err := LoadMerged("", useLocal)
			if err != nil {
				t.Fatalf("LoadMerged err = %v, want warnings, not an error", err)
			}
			if tc.want == nil {
				if len(cfg.Warnings) != 0 {
					t.Fatalf("warnings = %q, want none", cfg.Warnings)
				}
				return
			}
			if len(cfg.Warnings) != 1 {
				t.Fatalf("warnings = %q, want exactly one", cfg.Warnings)
			}
			for _, want := range tc.want {
				if !strings.Contains(cfg.Warnings[0], want) {
					t.Fatalf("warning %q does not mention %q", cfg.Warnings[0], want)
				}
			}
			for _, unwanted := range tc.notWant {
				if strings.Contains(cfg.Warnings[0], unwanted) {
					t.Fatalf("warning %q should not mention %q", cfg.Warnings[0], unwanted)
				}
			}
		})
	}
}

// A stat failure other than not-exist must not silently disable the check: the
// content may be there and unverifiable, so the load warns. A remotes dir that
// is a regular file makes the content path stat fail with ENOTDIR — a
// permission failure takes the same branch.
func TestDivergenceUninspectableContentWarns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a path component that is a file maps to not-exist on Windows")
	}
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	remotes := filepath.Join(t.TempDir(), "remotes")
	if err := os.WriteFile(remotes, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer remotesDirIn(remotes)()

	if err := os.MkdirAll(filepath.Join(xdg, "enver"), 0o755); err != nil {
		t.Fatal(err)
	}
	record := "remotes:\n  llm:\n    url: https://example.com/c.yaml\n"
	if err := os.WriteFile(filepath.Join(xdg, "enver", "config.yaml"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadMerged("", false)
	if err != nil {
		t.Fatalf("LoadMerged err = %v, want a warning, not an error", err)
	}
	if len(cfg.Warnings) != 1 {
		t.Fatalf("warnings = %q, want exactly one", cfg.Warnings)
	}
	for _, want := range []string{"cannot be inspected", "enver pull llm"} {
		if !strings.Contains(cfg.Warnings[0], want) {
			t.Fatalf("warning %q does not mention %q", cfg.Warnings[0], want)
		}
	}
}

// loadProjectFixture parses content the way a pull lands it, so projection is
// exercised against a decoded file, marks and comments included.
func loadProjectFixture(t *testing.T, body string) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "content.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile err = %v", err)
	}
	return cfg
}

// Projection is the whole namespace story for remote content: profiles move
// under the record's prefix, and every extends target inside the file's own
// namespace — the file's own profiles and the records its own remotes: section
// declares — moves with them. A target outside that namespace is an error: the
// file may not bind the pulling user's profiles or another record's projection.
// The two sections a remote author must never own — default and remotes — are
// dropped.
func TestProject(t *testing.T) {
	t.Run("rename, rewrite and drops", func(t *testing.T) {
		cfg := loadProjectFixture(t, `default: ollama
remotes:
  injected:
    url: https://attacker.example/evil.yaml
  claude:
    url: https://example.com/claude.yaml
profiles:
  ollama:
    unset: [LEGACY_HOST]
    env:
      OLLAMA_HOST: http://localhost:11434
      ~OLLAMA_TOKEN: s3cret
  qwen:
    extends: ollama
    env:
      MODEL: qwen2.5-coder:32b
  opus:
    extends: claude/default
    env:
      ROLE: opus
`)
		proj, err := Project(cfg, "llm", false)
		if err != nil {
			t.Fatalf("Project err = %v, want a projection", err)
		}

		if got, want := slices.Sorted(maps.Keys(proj.Profiles)), []string{"llm/ollama", "llm/opus", "llm/qwen"}; !slices.Equal(got, want) {
			t.Fatalf("projected profiles = %v, want %v", got, want)
		}
		wantExtends := Extends{"llm/ollama"}
		if got := proj.Profiles["llm/qwen"].Extends; !slices.Equal(got, wantExtends) {
			t.Fatalf("llm/qwen extends = %v, want %v (same-file target rewritten)", got, wantExtends)
		}
		if got, want := proj.Profiles["llm/opus"].Extends, (Extends{"llm/claude/default"}); !slices.Equal(got, want) {
			t.Fatalf("llm/opus extends = %v, want %v (declared child rewritten)", got, want)
		}

		ollama := proj.Profiles["llm/ollama"]
		// An unset entry names an env key, never a profile: it stays as authored.
		wantUnset := Unsets{"LEGACY_HOST"}
		if !slices.Equal(ollama.Unset, wantUnset) {
			t.Fatalf("llm/ollama unset = %v, want %v", ollama.Unset, wantUnset)
		}
		if want := map[string]string{"OLLAMA_HOST": "http://localhost:11434", "OLLAMA_TOKEN": "s3cret"}; !maps.Equal(ollama.Env, want) {
			t.Fatalf("llm/ollama env = %v, want %v", ollama.Env, want)
		}
		// The mark is not a secret: it rides through so a patch written over the
		// remote value stays masked and remains an encrypt candidate.
		if !ollama.Marked["OLLAMA_TOKEN"] || len(ollama.Marked) != 1 {
			t.Fatalf("llm/ollama marked = %v, want only OLLAMA_TOKEN", ollama.Marked)
		}

		if proj.Default != "" {
			t.Fatalf("projected default = %q, want dropped", proj.Default)
		}
		if len(proj.Remotes) != 0 {
			t.Fatalf("projected remotes = %v, want dropped: a remote can never register a fetch", proj.Remotes)
		}
		if got := proj.ProfileLayer["llm/ollama"]; got != "remote:llm" {
			t.Fatalf("llm/ollama layer = %q, want remote:llm", got)
		}

		// The input is the pulled file's parsed view; projection never edits it.
		if _, ok := cfg.Profiles["ollama"]; !ok {
			t.Fatal("projection removed the source profile from its input")
		}
		if _, ok := cfg.Remotes["injected"]; !ok {
			t.Fatal("projection removed the source remotes section from its input")
		}
	})

	t.Run("out-of-namespace targets are an error", func(t *testing.T) {
		cases := []struct {
			name   string
			target string
		}{
			{"a user profile", "provider"},
			{"another record's projection", "other/base"},
			{"the pulled record's own name", "llm/base"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				cfg := Config{Profiles: map[string]Profile{
					"child": {Extends: Extends{tc.target}, Env: map[string]string{"J": "w"}},
				}}
				proj, err := Project(cfg, "llam", false)
				if err == nil || !strings.Contains(err.Error(), "outside this file") || !strings.Contains(err.Error(), tc.target) {
					t.Fatalf("Project = %+v, %v; want an error naming the out-of-namespace target %q", proj, err, tc.target)
				}
			})
		}
	})

	t.Run("control characters", func(t *testing.T) {
		content := Config{
			Remotes: map[string]RemoteRecord{"claude": {URL: "https://example.com/claude.yaml"}},
			Profiles: map[string]Profile{
				"ol\x1blama\n": {
					Extends:  Extends{"ol\x1blama2\r\n", "cl\x01aude/def\rault"},
					Unset:    Unsets{"LEG\x07ACY"},
					Env:      map[string]string{"HOST\x1b": "http://x\x07y", "TLS\x1b": "line1\nline2"},
					Comments: map[string]string{"HOST\x1b": "hi\x1b[31mred\x07\n# second line"},
					Marked:   map[string]bool{"HOST\x1b": true},
				},
				"ol\x1blama2": {Env: map[string]string{"K": "v"}},
			},
		}
		proj, err := Project(content, "llm", false)
		if err != nil {
			t.Fatalf("Project err = %v, want a projection", err)
		}

		p, ok := proj.Profiles["llm/ollama"]
		if !ok {
			t.Fatalf("projected profiles = %v, want a cleaned llm/ollama", slices.Sorted(maps.Keys(proj.Profiles)))
		}
		// A name never carries a line break: it reaches list rendering,
		// completions and the TUI, where an embedded newline is line injection.
		wantExtends := Extends{"llm/ollama2", "llm/claude/default"}
		if !slices.Equal(p.Extends, wantExtends) {
			t.Fatalf("extends = %v, want %v", p.Extends, wantExtends)
		}
		wantUnset := Unsets{"LEGACY"}
		if !slices.Equal(p.Unset, wantUnset) {
			t.Fatalf("unset = %v, want %v", p.Unset, wantUnset)
		}
		// Value and comment text is data, so its newlines survive: a multi-line
		// value (a PEM bundle) and a multi-line comment are structure, not
		// terminal escapes.
		if want := map[string]string{"HOST": "http://xy", "TLS": "line1\nline2"}; !maps.Equal(p.Env, want) {
			t.Fatalf("env = %v, want %v", p.Env, want)
		}
		if got, want := p.Comments["HOST"], "hi[31mred\n# second line"; got != want {
			t.Fatalf("comment = %q, want %q", got, want)
		}
		if !p.Marked["HOST"] || len(p.Marked) != 1 {
			t.Fatalf("marked = %v, want only HOST", p.Marked)
		}
	})

	t.Run("no prefix keeps upstream names bare", func(t *testing.T) {
		cfg := loadProjectFixture(t, `default: ollama
remotes:
  claude:
    url: https://example.com/claude.yaml
profiles:
  ollama:
    env:
      OLLAMA_HOST: http://localhost:11434
  qwen:
    extends: ollama
    env:
      MODEL: qwen2.5-coder:32b
  opus:
    extends: claude/default
    env:
      ROLE: opus
`)
		proj, err := Project(cfg, "llm", true)
		if err != nil {
			t.Fatalf("Project err = %v, want a projection", err)
		}

		if got, want := slices.Sorted(maps.Keys(proj.Profiles)), []string{"ollama", "opus", "qwen"}; !slices.Equal(got, want) {
			t.Fatalf("projected profiles = %v, want bare upstream names %v", got, want)
		}
		if got, want := proj.Profiles["qwen"].Extends, (Extends{"ollama"}); !slices.Equal(got, want) {
			t.Fatalf("qwen extends = %v, want %v (bare target passes through)", got, want)
		}
		if got, want := proj.Profiles["opus"].Extends, (Extends{"claude/default"}); !slices.Equal(got, want) {
			t.Fatalf("opus extends = %v, want %v (child-prefixed target passes through)", got, want)
		}
		// Provenance is the record either way: a bare-name pull still came from
		// remote llm, and masking/fence rules key off the layer.
		if got := proj.ProfileLayer["ollama"]; got != "remote:llm" {
			t.Fatalf("ollama layer = %q, want remote:llm", got)
		}
		if proj.Default != "" || len(proj.Remotes) != 0 {
			t.Fatalf("projected default/remotes = %q/%v, want both dropped", proj.Default, proj.Remotes)
		}
	})

	t.Run("names that clean to one profile resolve deterministically", func(t *testing.T) {
		// Two upstream names differing only by a control character clean to one
		// projected name. The fold must not depend on map iteration order, so the
		// later name in sorted order wins, as a later entry does everywhere else.
		content := Config{Profiles: map[string]Profile{
			"shar\x1bed": {Env: map[string]string{"K": "injected"}},
			"shared":     {Env: map[string]string{"K": "honest"}},
		}}
		for range 16 {
			proj, err := Project(content, "llm", false)
			if err != nil {
				t.Fatalf("Project err = %v", err)
			}
			if got, want := proj.Profiles["llm/shared"].Env["K"], "honest"; got != want {
				t.Fatalf("llm/shared K = %q, want %q (the later sorted name must win every run)", got, want)
			}
		}
	})
}

// Two bare-name remotes defining one profile name fold in sorted record order,
// so the merged view is the same on every run and every machine. The winner is
// never silent: the load reports the collision, since the losing record's
// version of that profile is unreachable.
func TestNoPrefixFoldDeterminism(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	remotes := t.TempDir()
	defer remotesDirIn(remotes)()

	if err := os.MkdirAll(filepath.Join(xdg, "enver"), 0o755); err != nil {
		t.Fatal(err)
	}
	record := "remotes:\n" +
		"  zeta:\n    url: https://example.com/zeta.yaml\n    no_prefix: true\n" +
		"  alpha:\n    url: https://example.com/alpha.yaml\n    no_prefix: true\n"
	if err := os.WriteFile(filepath.Join(xdg, "enver", "config.yaml"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"alpha": "from-alpha", "zeta": "from-zeta"} {
		body := "profiles:\n  base:\n    env:\n      K: " + value + "\n"
		if err := os.WriteFile(filepath.Join(remotes, name+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		// A matching sidecar keeps the fixture to one warning: the collision.
		if err := WriteRemoteState(name, RemoteState{URL: "https://example.com/" + name + ".yaml"}); err != nil {
			t.Fatal(err)
		}
	}

	for range 16 {
		cfg, err := LoadMerged("", false)
		if err != nil {
			t.Fatalf("LoadMerged err = %v", err)
		}
		if got, want := cfg.Profiles["base"].Env["K"], "from-zeta"; got != want {
			t.Fatalf("base.K = %q, want %q (the later record in sorted order wins)", got, want)
		}
		// The layer is the winning record, so provenance names what actually
		// supplied the value rather than merely "a remote".
		if got, want := cfg.ProfileLayer["base"], "remote:zeta"; got != want {
			t.Fatalf("base layer = %q, want %q", got, want)
		}
		if len(cfg.Warnings) != 1 {
			t.Fatalf("warnings = %q, want exactly the collision", cfg.Warnings)
		}
		for _, want := range []string{`"zeta"`, `"alpha"`, `"base"`, "sorted", "wins"} {
			if !strings.Contains(cfg.Warnings[0], want) {
				t.Fatalf("warning %q does not mention %q", cfg.Warnings[0], want)
			}
		}
	}
}
