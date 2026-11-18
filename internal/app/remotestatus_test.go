package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neiromaster/enver/internal/config"
)

// TestRemoteStatuses pins the freshness rows enver pull --list renders: sorted
// records, the source each resolves to, the pin state, and the commit and time
// the sidecar recorded. A record no sidecar vouches for and a sidecar whose
// source the record moved away from are told apart.
func TestRemoteStatuses(t *testing.T) {
	f := newPullFixture(t)
	pulledAt := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	f.write(t, f.globalPath, `remotes:
  pinned:
    url: github:someone/llm
    ref: v2
  floating:
    url: https://example.com/float.yaml
  moved:
    url: https://example.com/new.yaml
  never:
    url: https://example.com/never.yaml
`)
	for _, name := range []string{"floating", "moved"} {
		if err := config.WriteRemoteContent(name, []byte(goodRemoteContent)); err != nil {
			t.Fatal(err)
		}
	}
	for name, st := range map[string]config.RemoteState{
		"pinned":   {URL: "https://raw.githubusercontent.com/someone/llm/v2/enver.yaml", Ref: "v2", Commit: "aaa111", PulledAt: pulledAt},
		"floating": {URL: "https://example.com/float.yaml", Commit: "bbb222", PulledAt: pulledAt},
		"moved":    {URL: "https://example.com/old.yaml", Commit: "ccc333", PulledAt: pulledAt},
	} {
		if err := config.WriteRemoteState(name, st); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.LoadMerged("", true)
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}

	rows := RemoteStatuses(cfg)
	if got := len(rows); got != 4 {
		t.Fatalf("rows = %d, want one per record; %+v", got, rows)
	}
	want := []string{"floating", "moved", "never", "pinned"}
	for i, name := range want {
		if rows[i].Name != name {
			t.Fatalf("row %d = %q, want %q (sorted); %+v", i, rows[i].Name, name, rows)
		}
	}

	floating := rows[0]
	if floating.Source != "https://example.com/float.yaml" || floating.Pinned || floating.Ref != "" {
		t.Errorf("floating = %+v, want the record's spelling and no pin", floating)
	}
	if !floating.Stored || !floating.Pulled || floating.Commit != "bbb222" || !floating.PulledAt.Equal(pulledAt) {
		t.Errorf("floating provenance = %+v, want the sidecar's commit and time", floating)
	}
	if floating.Stale {
		t.Errorf("floating = %+v, want no staleness: the sidecar names the record's own source", floating)
	}

	moved := rows[1]
	if !moved.Pulled || moved.Commit != "ccc333" {
		t.Errorf("moved provenance = %+v, want the sidecar's last pull", moved)
	}
	if !moved.Stale || moved.StaleFrom != "https://example.com/old.yaml" {
		t.Errorf("moved = %+v, want the sidecar's source marked stale", moved)
	}

	never := rows[2]
	if never.Pulled || never.Stored || never.Commit != "" || !never.PulledAt.IsZero() {
		t.Errorf("never = %+v, want a record with no pull state and no content", never)
	}

	pinned := rows[3]
	if !pinned.Pinned || pinned.Ref != "v2" || pinned.Source != "github:someone/llm at ref v2" {
		t.Errorf("pinned = %+v, want the spelling restated with its ref", pinned)
	}
	if !pinned.Pulled || pinned.Commit != "aaa111" {
		t.Errorf("pinned provenance = %+v, want the sidecar's commit", pinned)
	}
}

// A listing is read-only: no content, sidecar, or config byte may change, since
// the freshness view is exactly what a later resolve would see.
func TestRemoteStatusesReadOnly(t *testing.T) {
	f := newPullFixture(t)
	f.write(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/llm.yaml\n")
	f.write(t, f.localPath, "profiles:\n  llm/base:\n    env:\n      KEY: value\n")
	if err := config.WriteRemoteContent("llm", []byte(goodRemoteContent)); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/llm.yaml", Commit: "aaa111", PulledAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	snapshot := func(t *testing.T) map[string]string {
		t.Helper()
		out := map[string]string{}
		paths := []string{f.globalPath, f.localPath, config.RemoteContentPath("llm"), config.RemoteStatePath("llm")}
		entries, err := os.ReadDir(f.remotesDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			paths = append(paths, filepath.Join(f.remotesDir, e.Name()))
		}
		for _, path := range paths {
			data, ok := readOrMissing(t, path)
			if !ok {
				data = "\x00missing"
			}
			out[path] = data
		}
		return out
	}
	before := snapshot(t)

	cfg, err := config.LoadMerged("", true)
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if rows := RemoteStatuses(cfg); len(rows) != 1 || !rows[0].Pulled {
		t.Fatalf("rows = %+v, want the declared record with its pull state", rows)
	}

	after := snapshot(t)
	if len(after) != len(before) {
		t.Fatalf("listing changed the remotes directory: %d files before, %d after", len(before), len(after))
	}
	for path, data := range before {
		if after[path] != data {
			t.Fatalf("listing changed %s", path)
		}
	}
}

// TestRemoteStatusesTree pins the tree a listing walks: every stored
// declaration becomes a row keyed by its full record path, one depth below its
// declarer, depth-first with siblings sorted — the order the pull summary and
// the printout both render.
func TestRemoteStatusesTree(t *testing.T) {
	f := newPullFixture(t)
	f.write(t, f.globalPath, `remotes:
  flat:
    url: https://example.com/flat.yaml
  tree:
    url: https://example.com/tree.yaml
`)
	for path, content := range map[string]string{
		"flat":            goodRemoteContent,
		"tree":            "profiles:\n  top:\n    env:\n      K: v\nremotes:\n  alpha:\n    url: https://example.com/alpha.yaml\n",
		"tree/alpha":      "profiles:\n  mid:\n    env:\n      K: v\nremotes:\n  deep:\n    url: https://example.com/deep.yaml\n",
		"tree/alpha/deep": "profiles:\n  bottom:\n    env:\n      K: v\n",
	} {
		if err := config.WriteRemoteContent(path, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.LoadMerged("", true)
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}

	rows := RemoteStatuses(cfg)
	want := []struct {
		name  string
		depth int
	}{
		{"flat", 1},
		{"tree", 1},
		{"tree/alpha", 2},
		{"tree/alpha/deep", 3},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %d, want %d: %+v", len(rows), len(want), rows)
	}
	for i, w := range want {
		if rows[i].Name != w.name || rows[i].Depth != w.depth {
			t.Errorf("row %d = (%q, depth %d), want (%q, depth %d)", i, rows[i].Name, rows[i].Depth, w.name, w.depth)
		}
	}

	t.Run("a declaration naming an ancestor does not loop the walk", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.globalPath, "remotes:\n  loop:\n    url: https://example.com/loop.yaml\n")
		for path, content := range map[string]string{
			"loop":           "profiles:\n  a:\n    env:\n      K: v\nremotes:\n  next:\n    url: https://example.com/next.yaml\n",
			"loop/next":      "profiles:\n  b:\n    env:\n      K: v\nremotes:\n  back:\n    url: https://example.com/back.yaml\n",
			"loop/next/back": "profiles:\n  c:\n    env:\n      K: v\nremotes:\n  loop:\n    url: https://example.com/loop.yaml\n",
		} {
			if err := config.WriteRemoteContent(path, []byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		cfg, err := config.LoadMerged("", true)
		if err != nil {
			t.Fatalf("LoadMerged: %v", err)
		}
		var names []string
		for _, row := range RemoteStatuses(cfg) {
			names = append(names, row.Name)
		}
		// The ancestor-named declaration is a deeper path, and its missing
		// content is where the walk ends — a row like any declared-but-unpulled
		// record, never a recursion.
		if got := strings.Join(names, ","); got != "loop,loop/next,loop/next/back,loop/next/back/loop" {
			t.Fatalf("rows = %q, want each declared record once, in tree order", got)
		}
	})
}
