package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemotesRecordMerge(t *testing.T) {
	base := Config{Remotes: map[string]RemoteRecord{
		"llm":    {URL: "github:someone/llm-profiles", Ref: "v1"},
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
	if base.Remotes["llm"].Ref != "v1" {
		t.Fatalf("base remotes mutated: %+v", base.Remotes)
	}
	got.Remotes["legacy"] = RemoteRecord{URL: "https://example.com/new.yaml"}
	if base.Remotes["legacy"].URL != "https://example.com/old.yaml" {
		t.Fatal("merged remotes map aliases the base map")
	}

	// The record is the unit: an overriding record with no ref clears the
	// inherited ref rather than inheriting it.
	partial := Merge(base, Config{Remotes: map[string]RemoteRecord{
		"llm": {URL: "https://example.com/llm.yaml"},
	}}, LayerGlobal, LayerLocal)
	if r := partial.Remotes["llm"]; r != (RemoteRecord{URL: "https://example.com/llm.yaml"}) {
		t.Fatalf("merged llm = %+v, want no inherited ref", r)
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
	if _, err := os.Stat(filepath.Join(dir, "llm.meta")); err != nil {
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
	}{
		{
			name:    "record re-pointed at another source",
			record:  "remotes:\n  llm:\n    url: https://example.com/b.yaml\n    ref: v3\n",
			content: true,
			state:   &RemoteState{URL: "https://example.com/a.yaml", Ref: "v2"},
			want:    []string{"llm", "https://example.com/a.yaml", "v2", "https://example.com/b.yaml", "v3", "enver pull llm"},
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
		})
	}
}
