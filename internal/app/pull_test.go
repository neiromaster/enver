package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/neiromaster/enver/internal/config"
)

// goodRemoteContent is a minimal valid remote: two profiles, one extending the
// other inside the same file, so projection has something to rename.
const goodRemoteContent = `profiles:
  ollama:
    env:
      OLLAMA_HOST: http://localhost:11434
  qwen:
    extends: ollama
    env:
      MODEL: qwen2.5-coder
`

// pullFixture isolates the three places a pull reads and writes: the global
// config (XDG_CONFIG_HOME), the local .enver.yaml (cwd), and the remotes
// directory.
type pullFixture struct {
	globalPath string
	localPath  string
	remotesDir string
}

func newPullFixture(t *testing.T) *pullFixture {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	remotes := t.TempDir()
	prev := config.RemotesDir
	config.RemotesDir = func() string { return remotes }
	t.Cleanup(func() { config.RemotesDir = prev })

	proj := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(proj); err != nil {
		t.Fatal(err)
	}
	// LIFO: restore cwd before TempDir cleanup removes it (Windows cannot
	// remove a directory that is still the process cwd).
	t.Cleanup(func() { _ = os.Chdir(wd) })

	if err := os.MkdirAll(filepath.Join(xdg, "enver"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &pullFixture{
		globalPath: filepath.Join(xdg, "enver", "config.yaml"),
		localPath:  filepath.Join(proj, config.LocalFilename),
		remotesDir: remotes,
	}
}

func (f *pullFixture) write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// options loads the view and both user files the way the CLI does, with the
// merged config taken from LoadMerged so remote base layers are in it.
func (f *pullFixture) options(t *testing.T, targetIsGlobal bool, fetch func(url, ref string) ([]byte, string, error)) PullOptions {
	t.Helper()
	merged, err := config.LoadMerged("", true)
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	targetPath, otherPath := f.globalPath, f.localPath
	if !targetIsGlobal {
		targetPath, otherPath = f.localPath, f.globalPath
	}
	target, err := config.LoadFile(targetPath)
	if err != nil {
		t.Fatalf("load target: %v", err)
	}
	other, err := config.LoadFile(otherPath)
	if err != nil {
		t.Fatalf("load other: %v", err)
	}
	return PullOptions{
		TargetIsGlobal: targetIsGlobal,
		TargetPath:     targetPath,
		OtherPath:      otherPath,
		Merged:         merged,
		Target:         target,
		Other:          other,
		Fetch:          fetch,
	}
}

func (f *pullFixture) state(t *testing.T, name string) (config.RemoteState, bool) {
	t.Helper()
	return config.ReadRemoteState(name)
}

func (f *pullFixture) remotes(t *testing.T, path string) map[string]config.RemoteRecord {
	t.Helper()
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return cfg.Remotes
}

func readOrMissing(t *testing.T, path string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data), true
}

func TestPullRegistersAndFetches(t *testing.T) {
	t.Run("prefixed registration", func(t *testing.T) {
		f := newPullFixture(t)
		var gotURL, gotRef string
		opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
			gotURL, gotRef = url, ref
			return []byte(goodRemoteContent), "9f2c1ab", nil
		})

		summary, err := Pull("github:someone/llm-profiles@v2", "llm", opts)
		if err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if want := "https://raw.githubusercontent.com/someone/llm-profiles/v2/enver.yaml"; gotURL != want {
			t.Fatalf("fetched URL = %q, want %q", gotURL, want)
		}
		if gotRef != "v2" {
			t.Fatalf("fetched ref = %q, want v2", gotRef)
		}

		// The record keeps the source spelling with the ref split into its own
		// field, so a later ref edit re-targets the fetch.
		rec, ok := f.remotes(t, f.globalPath)["llm"]
		if !ok {
			t.Fatalf("record llm not written to %s", f.globalPath)
		}
		if rec.URL != "github:someone/llm-profiles" || rec.Ref != "v2" || rec.NoPrefix {
			t.Fatalf("record = %+v, want url github:someone/llm-profiles, ref v2, prefixed", rec)
		}

		content, ok := readOrMissing(t, config.RemoteContentPath("llm"))
		if !ok {
			t.Fatal("content file not written")
		}
		if content != goodRemoteContent {
			t.Fatalf("content = %q, want the fetched bytes verbatim", content)
		}
		st, ok := f.state(t, "llm")
		if !ok {
			t.Fatal("sidecar not written")
		}
		if st.URL != gotURL || st.Ref != "v2" || st.Commit != "9f2c1ab" {
			t.Fatalf("state = %+v, want the resolved source, ref and commit", st)
		}
		if st.PulledAt.IsZero() {
			t.Fatal("state has no pulled-at time")
		}
		for _, want := range []string{`remote "llm"`, "github:someone/llm-profiles", "v2", "llm/ollama", "llm/qwen"} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary misses %q:\n%s", want, summary)
			}
		}
	})

	t.Run("no-prefix registration persists the mode and warns on collisions", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.globalPath, "profiles:\n  base:\n    env:\n      LOCAL: keep\n")
		f.write(t, f.localPath, "profiles:\n  base:\n    env:\n      LOCAL: keep\n")
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte("profiles:\n  base:\n    env:\n      REMOTE: pulled\n"), "", nil
		})
		opts.NoPrefix = true

		summary, err := Pull("https://example.com/base.yaml", "team", opts)
		if err != nil {
			t.Fatalf("Pull: %v", err)
		}
		rec, ok := f.remotes(t, f.globalPath)["team"]
		if !ok {
			t.Fatal("record team not written")
		}
		if !rec.NoPrefix {
			t.Fatalf("record = %+v, want no_prefix persisted", rec)
		}
		// The gate must see the no-prefix mode, or the collision warning never
		// fires on a pull that is about to land on the user's own namespace.
		for _, want := range []string{"stored without a prefix", `"base"`, "will be overridden"} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary misses the no-prefix collision %q:\n%s", want, summary)
			}
		}
		if strings.Contains(summary, "team/base") {
			t.Errorf("summary lists a prefixed name for a no-prefix pull:\n%s", summary)
		}
	})
}

// An unpinned gist serves whatever the gist holds now, so a pull pins the
// revision the head lookup reports before fetching: the sidecar's commit then
// names the bytes actually stored. A pinned gist already names its revision and
// a plain https URL is not a gist source, so neither one looks up.
func TestPullGist(t *testing.T) {
	const (
		gistRevision    = "4fafbd300aaddbe4e16069d9fd25d4b5713c0130"
		unpinnedGistURL = "https://gist.githubusercontent.com/someone/abc123/raw/enver.yaml"
		pinnedGistURL   = "https://gist.githubusercontent.com/someone/abc123/raw/" + gistRevision + "/enver.yaml"
	)

	t.Run("unpinned gist pins the served revision", func(t *testing.T) {
		f := newPullFixture(t)
		var gotURL, gotRef string
		opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
			gotURL, gotRef = url, ref
			return []byte(goodRemoteContent), "", nil
		})
		opts.GistHead = func(string) (string, string) {
			return pinnedGistURL, gistRevision
		}

		summary, err := Pull("gist:someone/abc123", "llm", opts)
		if err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if gotURL != pinnedGistURL {
			t.Fatalf("fetched URL = %q, want %q", gotURL, pinnedGistURL)
		}
		if gotRef != "" {
			t.Fatalf("fetched ref = %q, want none; the revision is already a segment of the pinned URL", gotRef)
		}
		st, ok := f.state(t, "llm")
		if !ok {
			t.Fatal("sidecar not written")
		}
		if st.URL != unpinnedGistURL || st.Ref != "" || st.Commit != gistRevision {
			t.Fatalf("state = %+v, want the record source, no ref, and the served revision", st)
		}
		if !strings.Contains(summary, "commit: "+gistRevision) {
			t.Errorf("summary misses the pinned revision:\n%s", summary)
		}
	})

	t.Run("pinned gist makes no lookup", func(t *testing.T) {
		f := newPullFixture(t)
		var gotURL, gotRef string
		opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
			gotURL, gotRef = url, ref
			return []byte(goodRemoteContent), "", nil
		})
		opts.GistHead = func(string) (string, string) {
			t.Error("a pinned gist must not look up the head revision")
			return "", ""
		}

		if _, err := Pull("gist:someone/abc123@"+gistRevision, "llm", opts); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if gotURL != pinnedGistURL {
			t.Fatalf("fetched URL = %q, want %q", gotURL, pinnedGistURL)
		}
		if gotRef != gistRevision {
			t.Fatalf("fetched ref = %q, want the pinned revision", gotRef)
		}
		st, ok := f.state(t, "llm")
		if !ok {
			t.Fatal("sidecar not written")
		}
		if st.Commit != gistRevision {
			t.Fatalf("state = %+v, want the pinned revision recorded", st)
		}
	})

	t.Run("a silent API still pulls", func(t *testing.T) {
		f := newPullFixture(t)
		var gotURL string
		opts := f.options(t, true, func(url, _ string) ([]byte, string, error) {
			gotURL = url
			return []byte(goodRemoteContent), "", nil
		})
		opts.GistHead = func(string) (string, string) { return "", "" }

		if _, err := Pull("gist:someone/abc123", "llm", opts); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if gotURL != unpinnedGistURL {
			t.Fatalf("fetched URL = %q, want the revision-free %q", gotURL, unpinnedGistURL)
		}
		st, ok := f.state(t, "llm")
		if !ok {
			t.Fatal("sidecar not written")
		}
		if st.Commit != "" {
			t.Fatalf("state = %+v, want no commit recorded when the lookup stays silent", st)
		}
	})

	t.Run("an https gist URL is untouched", func(t *testing.T) {
		f := newPullFixture(t)
		var gotURL, gotRef string
		opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
			gotURL, gotRef = url, ref
			return []byte(goodRemoteContent), "", nil
		})
		opts.GistHead = func(string) (string, string) {
			t.Error("an https URL is not a gist source and must not be looked up")
			return "", ""
		}

		if _, err := Pull(unpinnedGistURL, "llm", opts); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if gotURL != unpinnedGistURL {
			t.Fatalf("fetched URL = %q, want %q fetched exactly as spelled", gotURL, unpinnedGistURL)
		}
		if gotRef != "" {
			t.Fatalf("fetched ref = %q, want none", gotRef)
		}
		st, ok := f.state(t, "llm")
		if !ok {
			t.Fatal("sidecar not written")
		}
		if st.URL != unpinnedGistURL || st.Ref != "" || st.Commit != "" {
			t.Fatalf("state = %+v, want the https URL with no ref and no commit", st)
		}
	})

	t.Run("a gist page names the gist form", func(t *testing.T) {
		f := newPullFixture(t)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte("<!DOCTYPE html><html></html>"), "", nil
		})

		_, err := Pull("https://gist.github.com/someone/abc123", "llm", opts)
		if err == nil {
			t.Fatal("Pull err = nil, want the gate to refuse a gist page")
		}
		for _, want := range []string{"gist page", "gist:<owner>/<id>"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("Pull err = %v, want it to carry %q", err, want)
			}
		}
	})
}

func TestPullRepointTakeoverConfirm(t *testing.T) {
	setup := func(t *testing.T) *pullFixture {
		t.Helper()
		f := newPullFixture(t)
		f.write(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/old.yaml\n")
		if err := config.WriteRemoteContent("llm", []byte(goodRemoteContent)); err != nil {
			t.Fatal(err)
		}
		if err := config.WriteRemoteState("llm", config.RemoteState{
			URL:      "https://example.com/old.yaml",
			PulledAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		f.write(t, f.localPath, "profiles:\n  llm/ollama:\n    env:\n      OLLAMA_API_KEY: sk-live\n")
		return f
	}

	t.Run("accepted", func(t *testing.T) {
		f := setup(t)
		var msg string
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte(goodRemoteContent), "", nil
		})
		opts.Confirm = func(m string, def bool) (bool, error) {
			msg = m
			if def {
				t.Error("the takeover prompt must default to no")
			}
			return true, nil
		}

		summary, err := Pull("https://example.com/new.yaml", "llm", opts)
		if err != nil {
			t.Fatalf("Pull: %v", err)
		}
		for _, want := range []string{"llm/ollama", "will now compose with content from", "https://example.com/new.yaml", "review before trusting it with your credentials"} {
			if !strings.Contains(msg, want) {
				t.Errorf("confirm message misses %q: %q", want, msg)
			}
		}
		for _, want := range []string{"source changed from", "https://example.com/old.yaml", "https://example.com/new.yaml"} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary misses %q:\n%s", want, summary)
			}
		}
		if rec := f.remotes(t, f.globalPath)["llm"]; rec.URL != "https://example.com/new.yaml" {
			t.Fatalf("record = %+v, want re-pointed", rec)
		}
		st, _ := f.state(t, "llm")
		if st.URL != "https://example.com/new.yaml" {
			t.Fatalf("state = %+v, want the new source", st)
		}
	})

	t.Run("declined writes nothing", func(t *testing.T) {
		f := setup(t)
		before, _ := readOrMissing(t, f.globalPath)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte(goodRemoteContent), "", nil
		})
		opts.Confirm = func(string, bool) (bool, error) { return false, nil }

		summary, err := Pull("https://example.com/new.yaml", "llm", opts)
		if err != nil {
			t.Fatalf("declined confirm must not error: %v", err)
		}
		if summary != "" {
			t.Errorf("declined confirm should print nothing, got %q", summary)
		}
		if after, _ := readOrMissing(t, f.globalPath); after != before {
			t.Fatal("declined confirm must not rewrite the config")
		}
		st, _ := f.state(t, "llm")
		if st.URL != "https://example.com/old.yaml" {
			t.Fatalf("state = %+v, want the old source untouched", st)
		}
	})

	t.Run("no confirm function is an error naming force", func(t *testing.T) {
		f := setup(t)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte(goodRemoteContent), "", nil
		})
		_, err := Pull("https://example.com/new.yaml", "llm", opts)
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("err = %v, want it to name --force", err)
		}
		if rec := f.remotes(t, f.globalPath)["llm"]; rec.URL != "https://example.com/old.yaml" {
			t.Fatalf("record = %+v, want no write on the non-interactive path", rec)
		}
	})

	t.Run("force skips the prompt and keeps the warning", func(t *testing.T) {
		f := setup(t)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte(goodRemoteContent), "", nil
		})
		opts.Force = true
		opts.Confirm = func(string, bool) (bool, error) {
			t.Error("--force must not prompt")
			return false, nil
		}
		summary, err := Pull("https://example.com/new.yaml", "llm", opts)
		if err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if !strings.Contains(summary, "will now compose with content from") {
			t.Errorf("forced pull must still report the takeover:\n%s", summary)
		}
		if rec := f.remotes(t, f.globalPath)["llm"]; rec.URL != "https://example.com/new.yaml" {
			t.Fatalf("record = %+v, want re-pointed", rec)
		}
	})

	// Flipping the pull mode is as invasive as a re-point: the patches stop
	// meeting the profiles they were written against, so it is confirmed the same
	// way.
	t.Run("mode change prompts like a re-point", func(t *testing.T) {
		f := setup(t)
		before, _ := readOrMissing(t, config.RemoteContentPath("llm"))
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte("profiles:\n  ollama:\n    env:\n      OLLAMA_HOST: http://localhost:11434\n"), "", nil
		})
		opts.NoPrefix = true
		var msg string
		opts.Confirm = func(m string, def bool) (bool, error) {
			msg = m
			if def {
				t.Error("the takeover prompt must default to no")
			}
			return true, nil
		}

		summary, err := Pull("https://example.com/old.yaml", "llm", opts)
		if err != nil {
			t.Fatalf("Pull: %v", err)
		}
		for _, want := range []string{"llm/ollama", "now stores bare names instead of prefixed names", "will no longer patch", "review before trusting it with your credentials"} {
			if !strings.Contains(msg, want) {
				t.Errorf("confirm message misses %q: %q", want, msg)
			}
		}
		if !strings.Contains(summary, "pull mode changed from prefixed names to bare names") {
			t.Errorf("summary misses the mode change:\n%s", summary)
		}
		if after, _ := readOrMissing(t, config.RemoteContentPath("llm")); after == before {
			t.Fatal("the accepted mode change must store the fetched content")
		}
		if rec := f.remotes(t, f.globalPath)["llm"]; !rec.NoPrefix {
			t.Fatalf("record = %+v, want the new mode stored", rec)
		}
	})

	t.Run("declined mode change writes nothing", func(t *testing.T) {
		f := setup(t)
		before, _ := readOrMissing(t, f.globalPath)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			t.Error("a declined registration must not fetch")
			return nil, "", nil
		})
		opts.NoPrefix = true
		opts.Confirm = func(string, bool) (bool, error) { return false, nil }

		summary, err := Pull("https://example.com/old.yaml", "llm", opts)
		if err != nil {
			t.Fatalf("declined confirm must not error: %v", err)
		}
		if summary != "" {
			t.Errorf("declined confirm should print nothing, got %q", summary)
		}
		if after, _ := readOrMissing(t, f.globalPath); after != before {
			t.Fatal("declined confirm must not rewrite the config")
		}
	})

	t.Run("mode change without a confirm function errors naming force", func(t *testing.T) {
		f := setup(t)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte(goodRemoteContent), "", nil
		})
		opts.NoPrefix = true
		_, err := Pull("https://example.com/old.yaml", "llm", opts)
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("err = %v, want it to name --force", err)
		}
		if rec := f.remotes(t, f.globalPath)["llm"]; rec.NoPrefix {
			t.Fatalf("record = %+v, want no write on the non-interactive path", rec)
		}
	})

	t.Run("force accepts a mode change and keeps the warning", func(t *testing.T) {
		f := setup(t)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte(goodRemoteContent), "", nil
		})
		opts.NoPrefix = true
		opts.Force = true
		opts.Confirm = func(string, bool) (bool, error) {
			t.Error("--force must not prompt")
			return false, nil
		}
		summary, err := Pull("https://example.com/old.yaml", "llm", opts)
		if err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if !strings.Contains(summary, "will no longer patch") {
			t.Errorf("forced mode change must still report the takeover:\n%s", summary)
		}
		if rec := f.remotes(t, f.globalPath)["llm"]; !rec.NoPrefix {
			t.Fatalf("record = %+v, want the new mode stored", rec)
		}
	})

	// The reverse direction is a different consequence: adding the prefix makes
	// existing name/... patches start meeting the record's profiles, where before
	// they sat on bare names.
	t.Run("bare-to-prefixed mode change says the patches start applying", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/llm.yaml\n    no_prefix: true\n")
		if err := config.WriteRemoteContent("llm", []byte(goodRemoteContent)); err != nil {
			t.Fatal(err)
		}
		if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/llm.yaml"}); err != nil {
			t.Fatal(err)
		}
		f.write(t, f.localPath, "profiles:\n  llm/ollama:\n    env:\n      OLLAMA_API_KEY: sk-live\n")
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte(goodRemoteContent), "", nil
		})
		var msg string
		opts.Confirm = func(m string, def bool) (bool, error) {
			msg = m
			if def {
				t.Error("the takeover prompt must default to no")
			}
			return true, nil
		}

		if _, err := Pull("https://example.com/llm.yaml", "llm", opts); err != nil {
			t.Fatalf("Pull: %v", err)
		}
		for _, want := range []string{"now stores prefixed names instead of bare names", "will now patch the remote's profiles under the prefix"} {
			if !strings.Contains(msg, want) {
				t.Errorf("confirm message misses %q: %q", want, msg)
			}
		}
		if strings.Contains(msg, "no longer patch") {
			t.Errorf("the prompt must not claim the patches stop applying when the prefix is added: %q", msg)
		}
	})

	// No patches, no takeover: the mode change is reported in the summary and the
	// pull proceeds without asking.
	t.Run("mode change without patches does not prompt", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/llm.yaml\n")
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte(goodRemoteContent), "", nil
		})
		opts.NoPrefix = true
		opts.Confirm = func(string, bool) (bool, error) {
			t.Error("no patch profiles means nothing to take over")
			return false, nil
		}
		summary, err := Pull("https://example.com/llm.yaml", "llm", opts)
		if err != nil {
			t.Fatalf("Pull: %v", err)
		}
		if !strings.Contains(summary, "pull mode changed from prefixed names to bare names") {
			t.Errorf("summary misses the mode change:\n%s", summary)
		}
	})
}

func TestPullGateFailureAtomic(t *testing.T) {
	setup := func(t *testing.T) *pullFixture {
		t.Helper()
		f := newPullFixture(t)
		f.write(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/old.yaml\n")
		if err := config.WriteRemoteContent("llm", []byte(goodRemoteContent)); err != nil {
			t.Fatal(err)
		}
		if err := config.WriteRemoteState("llm", config.RemoteState{
			URL:      "https://example.com/old.yaml",
			PulledAt: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}
		return f
	}
	snapshot := func(t *testing.T, f *pullFixture) [3]string {
		t.Helper()
		var out [3]string
		for i, path := range []string{f.globalPath, config.RemoteContentPath("llm"), config.RemoteStatePath("llm")} {
			data, ok := readOrMissing(t, path)
			if !ok {
				data = "\x00missing"
			}
			out[i] = data
		}
		return out
	}

	t.Run("fetch failure", func(t *testing.T) {
		f := setup(t)
		before := snapshot(t, f)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return nil, "", errors.New("connection reset")
		})
		if _, err := Pull("https://example.com/new.yaml", "llm", opts); err == nil {
			t.Fatal("a fetch failure must fail the pull")
		}
		if after := snapshot(t, f); after != before {
			t.Fatal("fetch failure changed config, content, or sidecar")
		}
	})

	t.Run("gate failure", func(t *testing.T) {
		f := setup(t)
		before := snapshot(t, f)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte("profiles:\n  bad/name:\n    env:\n      K: v\n"), "", nil
		})
		if _, err := Pull("https://example.com/new.yaml", "llm", opts); err == nil {
			t.Fatal("content the gate refuses must fail the pull")
		}
		if after := snapshot(t, f); after != before {
			t.Fatal("gate failure changed config, content, or sidecar")
		}
	})

	t.Run("update gate failure", func(t *testing.T) {
		f := setup(t)
		before := snapshot(t, f)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte("profiles:\n  bad/name:\n    env:\n      K: v\n"), "", nil
		})
		if _, err := PullUpdate("llm", opts); err == nil {
			t.Fatal("update must fail on content the gate refuses")
		}
		if after := snapshot(t, f); after != before {
			t.Fatal("failed update changed content or sidecar")
		}
	})
}

func TestPullRemoveScopesContent(t *testing.T) {
	t.Run("both layers declare keeps the content", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/global.yaml\n")
		f.write(t, f.localPath, "remotes:\n  llm:\n    url: https://example.com/local.yaml\nprofiles:\n  llm/ollama:\n    env:\n      OLLAMA_API_KEY: sk-live\n")
		if err := config.WriteRemoteContent("llm", []byte(goodRemoteContent)); err != nil {
			t.Fatal(err)
		}
		opts := f.options(t, false, nil)

		summary, err := PullRemove("llm", opts)
		if err != nil {
			t.Fatalf("PullRemove: %v", err)
		}
		if _, ok := f.remotes(t, f.localPath)["llm"]; ok {
			t.Fatal("the closest (local) declaration must be removed")
		}
		if _, ok := f.remotes(t, f.globalPath)["llm"]; !ok {
			t.Fatal("the surviving global declaration must stay")
		}
		if _, ok := readOrMissing(t, config.RemoteContentPath("llm")); !ok {
			t.Fatal("content must survive while a visible layer declares the record")
		}
		for _, want := range []string{`remote "llm"`, "local", "still declared in the global config", "https://example.com/global.yaml", "enver pull llm"} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary misses %q:\n%s", want, summary)
			}
		}
		if strings.Contains(summary, "no longer inherits") {
			t.Errorf("a surviving declaration means the patch is not orphaned:\n%s", summary)
		}
	})

	t.Run("pinned global removes that layer only", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/global.yaml\n")
		f.write(t, f.localPath, "remotes:\n  llm:\n    url: https://example.com/local.yaml\n")
		if err := config.WriteRemoteContent("llm", []byte(goodRemoteContent)); err != nil {
			t.Fatal(err)
		}
		opts := f.options(t, true, nil)
		opts.LayerPinned = true

		if _, err := PullRemove("llm", opts); err != nil {
			t.Fatalf("PullRemove: %v", err)
		}
		if _, ok := f.remotes(t, f.globalPath)["llm"]; ok {
			t.Fatal("the pinned global declaration must be removed")
		}
		if _, ok := f.remotes(t, f.localPath)["llm"]; !ok {
			t.Fatal("an explicit layer must not touch the other one")
		}
		if _, ok := readOrMissing(t, config.RemoteContentPath("llm")); !ok {
			t.Fatal("content must survive while the local layer still declares the record")
		}
	})

	t.Run("last declaration deletes content and reports orphan patches", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/global.yaml\n")
		f.write(t, f.localPath, "profiles:\n  llm/ollama:\n    env:\n      OLLAMA_API_KEY: sk-live\n")
		if err := config.WriteRemoteContent("llm", []byte(goodRemoteContent)); err != nil {
			t.Fatal(err)
		}
		if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/global.yaml"}); err != nil {
			t.Fatal(err)
		}
		opts := f.options(t, true, nil)

		summary, err := PullRemove("llm", opts)
		if err != nil {
			t.Fatalf("PullRemove: %v", err)
		}
		if len(f.remotes(t, f.globalPath)) != 0 {
			t.Fatal("the last declaration must be removed")
		}
		if _, ok := readOrMissing(t, config.RemoteContentPath("llm")); ok {
			t.Fatal("content must be deleted with the last declaration")
		}
		if _, ok := readOrMissing(t, config.RemoteStatePath("llm")); ok {
			t.Fatal("sidecar must be deleted with the last declaration")
		}
		for _, want := range []string{"llm/ollama", "no longer inherits", `remote "llm"`} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary misses the orphan patch note %q:\n%s", want, summary)
			}
		}
	})

	t.Run("declaration in the unpinned other layer is removed", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/global.yaml\n")
		opts := f.options(t, false, nil) // local is the target; only global declares
		summary, err := PullRemove("llm", opts)
		if err != nil {
			t.Fatalf("PullRemove: %v", err)
		}
		if len(f.remotes(t, f.globalPath)) != 0 {
			t.Fatal("removal must target the declaration that exists")
		}
		if !strings.Contains(summary, "global") {
			t.Errorf("summary should say the global layer was the one removed:\n%s", summary)
		}
	})

	t.Run("pinned layer without a declaration errors", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/global.yaml\n")
		opts := f.options(t, false, nil)
		opts.LayerPinned = true
		_, err := PullRemove("llm", opts)
		if err == nil || !strings.Contains(err.Error(), "global") {
			t.Fatalf("err = %v, want it to name the declaring layer", err)
		}
	})

	t.Run("no declaration errors", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.globalPath, "profiles:\n  p:\n    env:\n      A: 1\n")
		opts := f.options(t, true, nil)
		if _, err := PullRemove("llm", opts); err == nil {
			t.Fatal("removing an unregistered remote must fail")
		}
	})
}

func TestPullUpdateHonorsRecordNoPrefix(t *testing.T) {
	f := newPullFixture(t)
	f.write(t, f.globalPath, "remotes:\n  team:\n    url: https://example.com/base.yaml\n    no_prefix: true\n")
	f.write(t, f.localPath, "profiles:\n  base:\n    env:\n      LOCAL: keep\n")
	opts := f.options(t, true, func(string, string) ([]byte, string, error) {
		return []byte("profiles:\n  base:\n    env:\n      REMOTE: pulled\n"), "sha1", nil
	})

	summary, err := PullUpdate("team", opts)
	if err != nil {
		t.Fatalf("PullUpdate: %v", err)
	}
	if !strings.Contains(summary, "stored without a prefix") {
		t.Errorf("the record's no-prefix mode must reach the gate:\n%s", summary)
	}
	rec := f.remotes(t, f.globalPath)["team"]
	if !rec.NoPrefix {
		t.Fatalf("record = %+v, want the stored mode untouched by a re-pull", rec)
	}
	st, ok := f.state(t, "team")
	if !ok || st.Commit != "sha1" {
		t.Fatalf("state = %+v ok=%v, want the re-pull recorded", st, ok)
	}

	// A --no-prefix flag on an update would flip the mode; the record owns it.
	opts2 := f.options(t, true, func(string, string) ([]byte, string, error) {
		return []byte("profiles:\n  base:\n    env:\n      REMOTE: pulled\n"), "", nil
	})
	opts2.NoPrefix = true
	if _, err := PullUpdate("team", opts2); err == nil || !strings.Contains(err.Error(), "--no-prefix") {
		t.Fatalf("err = %v, want it to explain that --no-prefix belongs to registration", err)
	}
}

func TestPullAllFetchesEveryRecord(t *testing.T) {
	f := newPullFixture(t)
	f.write(t, f.globalPath, "remotes:\n  one:\n    url: https://example.com/one.yaml\n  two:\n    url: https://example.com/two.yaml\n")
	opts := f.options(t, true, func(url, _ string) ([]byte, string, error) {
		if strings.Contains(url, "two") {
			return nil, "", errors.New("boom")
		}
		return []byte(goodRemoteContent), "", nil
	})

	summary, err := PullAll(opts)
	if err == nil {
		t.Fatal("a failing record must fail the run")
	}
	if _, ok := f.state(t, "one"); !ok {
		t.Fatal("the record that succeeded should still be stored")
	}
	if _, ok := f.state(t, "two"); ok {
		t.Fatal("the record that failed must not be stored")
	}
	for _, want := range []string{`remote "one"`, `remote "two"`, "boom"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary misses %q:\n%s", want, summary)
		}
	}
}

func TestPullRejectsUnusableRecordNames(t *testing.T) {
	f := newPullFixture(t)
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"slash in the record name", "https://example.com/c.yaml", "not a valid record name"},
		{"plain http source", "http://example.com/c.yaml", "https"},
		{"windows device name", "https://example.com/c.yaml", "device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := f.options(t, true, func(string, string) ([]byte, string, error) {
				t.Error("an unusable record must fail before fetching")
				return nil, "", nil
			})
			name := "llm"
			switch tc.name {
			case "slash in the record name":
				name = "llm/other"
			case "windows device name":
				name = "nul"
			}
			_, err := Pull(tc.raw, name, opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if _, ok := f.remotes(t, f.globalPath)[name]; ok {
				t.Fatal("an unusable record must not be written")
			}
		})
	}

	t.Run("case-fold collision with the other layer", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.localPath, "remotes:\n  LLM:\n    url: https://example.com/other.yaml\n")
		opts := f.options(t, true, nil)
		_, err := Pull("https://example.com/c.yaml", "llm", opts)
		if err == nil || !strings.Contains(err.Error(), "LLM") {
			t.Fatalf("err = %v, want it to name the colliding record", err)
		}
		if _, ok := f.remotes(t, f.globalPath)["llm"]; ok {
			t.Fatal("a colliding record must not be written")
		}
	})

	// WriteRemotes rewrites the whole section and validates all of it, so a
	// sibling record that is already unusable has to fail the pull before the
	// fetch: the content and the sidecar would otherwise land for a record that
	// was never registered.
	t.Run("unusable sibling record fails before the fetch", func(t *testing.T) {
		f := newPullFixture(t)
		f.write(t, f.globalPath, "remotes:\n  broken:\n    url: http://example.com/broken.yaml\n")
		beforeConfig, _ := readOrMissing(t, f.globalPath)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			t.Error("an unwritable section must fail before the fetch")
			return nil, "", nil
		})

		_, err := Pull("https://example.com/new.yaml", "llm", opts)
		if err == nil || !strings.Contains(err.Error(), "broken") {
			t.Fatalf("err = %v, want it to name the unusable sibling", err)
		}
		if after, _ := readOrMissing(t, f.globalPath); after != beforeConfig {
			t.Fatal("the target config must be unchanged")
		}
		if _, ok := readOrMissing(t, config.RemoteContentPath("llm")); ok {
			t.Fatal("no content may be stored for a record that was never registered")
		}
		if _, ok := readOrMissing(t, config.RemoteStatePath("llm")); ok {
			t.Fatal("no sidecar may be stored for a record that was never registered")
		}
	})
}

// The gate scopes targets to the fetched file's own namespace, so a remote
// extending another record's projection is refused even when that record is
// declared and pulled in the user's config: a file that wants a foreign base
// must declare it in its own remotes section.
func TestPullRefusesOtherRecordTarget(t *testing.T) {
	f := newPullFixture(t)
	f.write(t, f.globalPath, "remotes:\n  other:\n    url: https://example.com/other.yaml\n")
	if err := config.WriteRemoteContent("other", []byte("profiles:\n  base:\n    env:\n      HOST: remote\n")); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRemoteState("other", config.RemoteState{URL: "https://example.com/other.yaml"}); err != nil {
		t.Fatal(err)
	}
	opts := f.options(t, true, func(string, string) ([]byte, string, error) {
		return []byte("profiles:\n  svc:\n    extends: other/base\n    env:\n      K: v\n"), "", nil
	})

	_, err := Pull("https://example.com/svc.yaml", "svc", opts)
	if err == nil || !strings.Contains(err.Error(), "outside this file") || !strings.Contains(err.Error(), `"other/base"`) {
		t.Fatalf("err = %v, want a refusal naming the out-of-namespace target", err)
	}
	if _, ok := readOrMissing(t, config.RemoteContentPath("svc")); ok {
		t.Fatal("a refused pull must not store content")
	}
	if _, ok := f.remotes(t, f.globalPath)["svc"]; ok {
		t.Fatal("a refused pull must not register the record")
	}
}

// A re-pull replaces the stored content whole: profiles the old fetch carried
// and the new one does not must disappear rather than linger in the projection.
func TestPullReplacesStoredContentWhole(t *testing.T) {
	f := newPullFixture(t)
	f.write(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/llm.yaml\n")
	if err := config.WriteRemoteContent("llm", []byte("profiles:\n  stale:\n    env:\n      OLD: gone\n")); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/llm.yaml"}); err != nil {
		t.Fatal(err)
	}
	opts := f.options(t, true, func(string, string) ([]byte, string, error) {
		return []byte(goodRemoteContent), "9f2c1ab", nil
	})

	summary, err := PullUpdate("llm", opts)
	if err != nil {
		t.Fatalf("PullUpdate: %v", err)
	}
	content, ok := readOrMissing(t, config.RemoteContentPath("llm"))
	if !ok || content != goodRemoteContent {
		t.Fatalf("content = %q, want the fetched bytes verbatim", content)
	}
	if strings.Contains(summary, "llm/stale") {
		t.Errorf("summary still lists a profile the new content dropped:\n%s", summary)
	}
	for _, want := range []string{"llm/ollama", "llm/qwen"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary misses %q:\n%s", want, summary)
		}
	}
	merged, err := config.LoadMerged("", true)
	if err != nil {
		t.Fatalf("LoadMerged: %v", err)
	}
	if _, ok := merged.Profiles["llm/stale"]; ok {
		t.Fatal("the merged view still projects a profile the re-pull replaced")
	}
}

// cloneFixture is the cloned-project scenario: the global layer declares llm
// with a secret patch profile under its prefix, the project file re-declares
// llm with its own source, and the stored content came from the global source.
func cloneFixture(t *testing.T) *pullFixture {
	t.Helper()
	f := newPullFixture(t)
	f.write(t, f.globalPath, `remotes:
  llm:
    url: https://example.com/global.yaml
profiles:
  llm/secret:
    extends: llm/base
    env:
      API_KEY: sk-live
`)
	f.write(t, f.localPath, "remotes:\n  llm:\n    url: https://example.com/project.yaml\n")
	if err := config.WriteRemoteContent("llm", []byte("profiles:\n  base:\n    env:\n      HOST: global\n")); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRemoteState("llm", config.RemoteState{
		URL:      "https://example.com/global.yaml",
		Commit:   "aaaa111",
		PulledAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

// A re-fetch replaces stored content whose sidecar names a source the effective
// record no longer points at. When patch profiles sit under the prefix that is
// the same takeover a re-registration is, and it must be confirmed before the
// fetch: the stored content is what the patches currently compose with.
func TestPullRefetchTakeoverFromStoredSource(t *testing.T) {
	t.Run("named re-fetch warns and a decline fetches nothing", func(t *testing.T) {
		f := cloneFixture(t)
		var msg string
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			t.Error("a declined takeover must not fetch")
			return nil, "", nil
		})
		opts.Confirm = func(m string, def bool) (bool, error) {
			msg = m
			if def {
				t.Error("the takeover prompt must default to no")
			}
			return false, nil
		}

		summary, err := PullUpdate("llm", opts)
		if err != nil {
			t.Fatalf("declined confirm must not error: %v", err)
		}
		if summary != "" {
			t.Errorf("declined confirm should print nothing, got %q", summary)
		}
		for _, want := range []string{`remote "llm"`, "llm/secret", "https://example.com/global.yaml", "https://example.com/project.yaml", "review before trusting it with your credentials"} {
			if !strings.Contains(msg, want) {
				t.Errorf("confirm message misses %q: %q", want, msg)
			}
		}
		if st, _ := f.state(t, "llm"); st.URL != "https://example.com/global.yaml" {
			t.Fatalf("state = %+v, want the stored source untouched", st)
		}
	})

	t.Run("non-interactive error names force", func(t *testing.T) {
		f := cloneFixture(t)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			t.Error("a gated re-fetch must not fetch")
			return nil, "", nil
		})
		_, err := PullUpdate("llm", opts)
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("err = %v, want it to name --force", err)
		}
		if !strings.Contains(err.Error(), "llm/secret") {
			t.Fatalf("err = %v, want it to name the patch profile", err)
		}
	})

	t.Run("force fetches and keeps the warning", func(t *testing.T) {
		f := cloneFixture(t)
		var fetches int
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			fetches++
			return []byte("profiles:\n  base:\n    env:\n      HOST: project\n"), "bbbb222", nil
		})
		opts.Force = true
		opts.Confirm = func(string, bool) (bool, error) {
			t.Error("--force must not prompt")
			return false, nil
		}

		summary, err := PullUpdate("llm", opts)
		if err != nil {
			t.Fatalf("PullUpdate: %v", err)
		}
		if fetches != 1 {
			t.Fatalf("fetches = %d, want 1", fetches)
		}
		for _, want := range []string{"stored content came from https://example.com/global.yaml", "instead of https://example.com/global.yaml", "will now compose with content from that source", "llm/secret"} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary misses %q:\n%s", want, summary)
			}
		}
		st, _ := f.state(t, "llm")
		if st.URL != "https://example.com/project.yaml" || st.Commit != "bbbb222" {
			t.Fatalf("state = %+v, want the fetched source and commit", st)
		}
	})

	t.Run("pull-all gates every record", func(t *testing.T) {
		f := cloneFixture(t)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			t.Error("pull-all must not fetch a gated record")
			return nil, "", nil
		})
		_, err := PullAll(opts)
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("err = %v, want a non-zero error naming --force", err)
		}
		if !strings.Contains(err.Error(), "llm/secret") {
			t.Fatalf("err = %v, want it to name the patch profile", err)
		}
		if st, _ := f.state(t, "llm"); st.URL != "https://example.com/global.yaml" {
			t.Fatalf("state = %+v, want the stored source untouched", st)
		}
	})

	t.Run("pull-all accepts under force", func(t *testing.T) {
		f := cloneFixture(t)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			return []byte("profiles:\n  base:\n    env:\n      HOST: project\n"), "bbbb222", nil
		})
		opts.Force = true
		summary, err := PullAll(opts)
		if err != nil {
			t.Fatalf("PullAll: %v", err)
		}
		if !strings.Contains(summary, "will now compose with content from") {
			t.Errorf("forced pull-all must still report the takeover:\n%s", summary)
		}
		if st, _ := f.state(t, "llm"); st.URL != "https://example.com/project.yaml" {
			t.Fatalf("state = %+v, want the fetched source", st)
		}
	})

	// A decline in a run over several records leaves that record as it was and
	// keeps the rest of the run going, rather than aborting the whole pull.
	t.Run("pull-all decline leaves the record and reports it", func(t *testing.T) {
		f := cloneFixture(t)
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			t.Error("a declined takeover must not fetch")
			return nil, "", nil
		})
		opts.Confirm = func(string, bool) (bool, error) { return false, nil }
		summary, err := PullAll(opts)
		if err != nil {
			t.Fatalf("a declined takeover must not fail the run: %v", err)
		}
		for _, want := range []string{`remote "llm" left unchanged`, "takeover declined"} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary misses %q:\n%s", want, summary)
			}
		}
		if st, _ := f.state(t, "llm"); st.URL != "https://example.com/global.yaml" {
			t.Fatalf("state = %+v, want the stored source untouched", st)
		}
	})

	t.Run("sidecar matching the record proceeds silently", func(t *testing.T) {
		f := cloneFixture(t)
		if err := config.WriteRemoteState("llm", config.RemoteState{
			URL:      "https://example.com/project.yaml",
			Commit:   "aaaa111",
			PulledAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		var fetches int
		opts := f.options(t, true, func(string, string) ([]byte, string, error) {
			fetches++
			return []byte("profiles:\n  base:\n    env:\n      HOST: project\n"), "cccc333", nil
		})
		opts.Confirm = func(string, bool) (bool, error) {
			t.Error("content from the record's own source is not a takeover")
			return false, nil
		}

		summary, err := PullUpdate("llm", opts)
		if err != nil {
			t.Fatalf("PullUpdate: %v", err)
		}
		if fetches != 1 {
			t.Fatalf("fetches = %d, want 1", fetches)
		}
		for _, unwanted := range []string{"stored content came from", "will now compose with content from"} {
			if strings.Contains(summary, unwanted) {
				t.Errorf("summary reports a takeover that did not happen (%q):\n%s", unwanted, summary)
			}
		}
	})

	// The merged view is the project's own declaration after a re-declaration, so
	// a registration must judge the change against the sidecar: re-registering
	// the source the project file already names still replaces content the global
	// source served, which is exactly the takeover a patch cares about.
	t.Run("registration against a poisoned merged view takes over against the sidecar", func(t *testing.T) {
		f := cloneFixture(t)
		opts := f.options(t, false, func(string, string) ([]byte, string, error) {
			t.Error("a gated registration must not fetch")
			return nil, "", nil
		})
		_, err := Pull("https://example.com/project.yaml", "llm", opts)
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("err = %v, want a non-zero error naming --force", err)
		}
		for _, want := range []string{"llm/secret", "instead of https://example.com/global.yaml"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want it to mention %q", err, want)
			}
		}
	})
}

// nestedOllamaContent is the motivating shape: a provider profile extending a
// base the file's own remotes: section declares, so one pull brings both.
const nestedOllamaContent = `profiles:
  glm-models:
    extends: claude/default
    env:
      MODEL: glm-4
remotes:
  claude:
    url: https://example.com/claude.yaml
`

// nestedClaudeContent is the base the provider declares: one profile the
// provider's extends target names.
const nestedClaudeContent = `profiles:
  default:
    env:
      API_BASE: https://api.example.com
`

// serveNested serves the two-level tree by URL, failing the test on anything
// else so an unexpected fetch is loud.
func serveNested(t *testing.T, url, ref string) ([]byte, string, error) {
	t.Helper()
	switch url {
	case "https://example.com/ollama.yaml":
		return []byte(nestedOllamaContent), "ollama-commit", nil
	case "https://example.com/claude.yaml":
		return []byte(nestedClaudeContent), "claude-commit", nil
	}
	t.Errorf("unexpected fetch of %s", url)
	return nil, "", nil
}

// remoteFiles lists every file under dir: the negative assertion a refused
// pull makes, since nothing may be stored when the walk or the tree check
// refuses.
func remoteFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPullFetchesNestedTree(t *testing.T) {
	f := newPullFixture(t)
	opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
		return serveNested(t, url, ref)
	})

	summary, err := Pull("https://example.com/ollama.yaml", "ollama", opts)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	for path, want := range map[string]string{
		"ollama":        nestedOllamaContent,
		"ollama/claude": nestedClaudeContent,
	} {
		content, ok := readOrMissing(t, config.RemoteContentPath(path))
		if !ok || content != want {
			t.Fatalf("content for %s = %q ok=%v, want the fetched bytes verbatim", path, content, ok)
		}
		st, ok := f.state(t, path)
		if !ok {
			t.Fatalf("sidecar for %s not written", path)
		}
		if st.Commit == "" {
			t.Fatalf("state for %s = %+v, want the fetch commit recorded", path, st)
		}
	}
	if fi, err := os.Stat(filepath.Join(f.remotesDir, "ollama")); err != nil || !fi.IsDir() {
		t.Fatalf("the nested record's directory was not created (stat err = %v)", err)
	}
	parent := strings.Index(summary, "✓ remote \"ollama\"")
	child := strings.Index(summary, "✓ remote \"ollama/claude\"")
	if parent < 0 || child < 0 || child < parent {
		t.Fatalf("summary does not render the parent block before the child:\n%s", summary)
	}
	if !strings.Contains(summary, "\n  ✓ remote \"ollama/claude\"") {
		t.Fatalf("the child block is not indented under its parent:\n%s", summary)
	}
	for _, want := range []string{
		"profiles: ollama/glm-models",
		"profiles: ollama/claude/default",
		"commit: ollama-commit",
		"commit: claude-commit",
	} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary misses %q:\n%s", want, summary)
		}
	}

	merged, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := merged.Profiles["ollama/glm-models"].Extends; len(got) != 1 || got[0] != "ollama/claude/default" {
		t.Fatalf("extends = %q, want ollama/claude/default", got)
	}
	r, err := merged.ResolveProfile("ollama/glm-models")
	if err != nil {
		t.Fatalf("resolve ollama/glm-models: %v", err)
	}
	if r.Env["MODEL"] != "glm-4" || r.Env["API_BASE"] != "https://api.example.com" {
		t.Fatalf("env = %q, want the profile's own key and the inherited base key", r.Env)
	}
}

func TestPullAsRenamesSubtree(t *testing.T) {
	f := newPullFixture(t)
	opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
		return serveNested(t, url, ref)
	})

	if _, err := Pull("https://example.com/ollama.yaml", "my-ollama", opts); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	for path, want := range map[string]string{
		"my-ollama":        nestedOllamaContent,
		"my-ollama/claude": nestedClaudeContent,
	} {
		content, ok := readOrMissing(t, config.RemoteContentPath(path))
		if !ok || content != want {
			t.Fatalf("content for %s = %q ok=%v, want the served bytes unchanged", path, content, ok)
		}
	}
	if _, ok := readOrMissing(t, config.RemoteContentPath("ollama")); ok {
		t.Fatal("the pull stored content under a name it was not registered as")
	}
	merged, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := merged.Profiles["my-ollama/glm-models"].Extends; len(got) != 1 || got[0] != "my-ollama/claude/default" {
		t.Fatalf("extends = %q, want the subtree re-composed under the new prefix", got)
	}
	if _, ok := merged.Profiles["my-ollama/claude/default"]; !ok {
		t.Fatal("the nested base did not land under the new prefix")
	}
}

func TestPullCycleRefused(t *testing.T) {
	t.Run("a loop of declarations", func(t *testing.T) {
		f := newPullFixture(t)
		opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
			switch url {
			case "https://example.com/a.yaml":
				return []byte("profiles:\n  a:\n    env:\n      K: v\nremotes:\n  b:\n    url: https://example.com/b.yaml\n"), "", nil
			case "https://example.com/b.yaml":
				return []byte("profiles:\n  b:\n    env:\n      J: w\nremotes:\n  a:\n    url: https://example.com/a.yaml\n"), "", nil
			}
			t.Errorf("unexpected fetch of %s", url)
			return nil, "", nil
		})

		_, err := Pull("https://example.com/a.yaml", "top", opts)
		if err == nil || !strings.Contains(err.Error(), "loops back") {
			t.Fatalf("err = %v, want the cycle refusal", err)
		}
		chain := "https://example.com/a.yaml -> https://example.com/b.yaml -> https://example.com/a.yaml"
		if !strings.Contains(err.Error(), chain) {
			t.Fatalf("err = %v, want the chain %q", err, chain)
		}
		if entries := remoteFiles(t, f.remotesDir); len(entries) != 0 {
			t.Fatalf("a refused cycle stored %q", entries)
		}
	})

	// A cycle is a source reappearing on one walk, not a source used twice:
	// the same source under two parents is two records with separate pristine
	// copies, exactly as two top-level records would be.
	t.Run("a shared source under two parents is not a cycle", func(t *testing.T) {
		f := newPullFixture(t)
		opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
			switch url {
			case "https://example.com/top.yaml":
				return []byte("profiles:\n  a:\n    env:\n      K: v\nremotes:\n  one:\n    url: https://example.com/mid1.yaml\n  two:\n    url: https://example.com/mid2.yaml\n"), "", nil
			case "https://example.com/mid1.yaml", "https://example.com/mid2.yaml":
				return []byte("profiles:\n  b:\n    env:\n      J: w\nremotes:\n  shared:\n    url: https://example.com/shared.yaml\n"), "", nil
			case "https://example.com/shared.yaml":
				return []byte("profiles:\n  c:\n    env:\n      L: x\n"), "", nil
			}
			t.Errorf("unexpected fetch of %s", url)
			return nil, "", nil
		})

		summary, err := Pull("https://example.com/top.yaml", "top", opts)
		if err != nil {
			t.Fatalf("Pull: %v", err)
		}
		for _, want := range []string{"top/one/shared", "top/two/shared"} {
			if _, ok := readOrMissing(t, config.RemoteContentPath(want)); !ok {
				t.Fatalf("record %s not stored as its own copy", want)
			}
			if !strings.Contains(summary, want) {
				t.Fatalf("summary misses %s:\n%s", want, summary)
			}
		}
		merged, err := Load(Options{})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if _, ok := merged.Profiles["top/one/shared/c"]; !ok {
			t.Fatal("both copies of the shared source must load")
		}
		if _, ok := merged.Profiles["top/two/shared/c"]; !ok {
			t.Fatal("both copies of the shared source must load")
		}
	})
}

func TestPullDepthCap(t *testing.T) {
	// One source past config.MaxRemoteDepth: the cap fires before the record
	// that would exceed it is fetched.
	const chainLen = 17
	f := newPullFixture(t)
	opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
		var n int
		if _, err := fmt.Sscanf(url, "https://example.com/s%d.yaml", &n); err != nil {
			t.Errorf("unexpected fetch of %s", url)
			return nil, "", nil
		}
		if n >= chainLen {
			t.Errorf("fetched %s, at or past the depth cap", url)
			return nil, "", nil
		}
		return []byte(fmt.Sprintf("profiles:\n  p%d:\n    env:\n      K: v\nremotes:\n  c:\n    url: https://example.com/s%d.yaml\n", n, n+1)), "", nil
	})

	_, err := Pull("https://example.com/s1.yaml", "deep", opts)
	if err == nil || !strings.Contains(err.Error(), "16 record cap") {
		t.Fatalf("err = %v, want the depth cap named", err)
	}
	for _, want := range []string{"https://example.com/s1.yaml", "https://example.com/s17.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want the chain to name %s", err, want)
		}
	}
	if entries := remoteFiles(t, f.remotesDir); len(entries) != 0 {
		t.Fatalf("a depth-capped pull stored %q", entries)
	}
}

func TestPullTreeCheckRefusesDangling(t *testing.T) {
	f := newPullFixture(t)
	opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
		switch url {
		case "https://example.com/ollama.yaml":
			return []byte(nestedOllamaContent), "", nil
		case "https://example.com/claude.yaml":
			return []byte("profiles:\n  base:\n    env:\n      K: v\n"), "", nil
		}
		t.Errorf("unexpected fetch of %s", url)
		return nil, "", nil
	})

	_, err := Pull("https://example.com/ollama.yaml", "ollama", opts)
	if err == nil {
		t.Fatal("a dangling subtree target must fail the pull")
	}
	for _, want := range []string{`remote "ollama"`, `"ollama/claude/default"`, "no profile in the pulled subtree defines"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to carry %q", err, want)
		}
	}
	if entries := remoteFiles(t, f.remotesDir); len(entries) != 0 {
		t.Fatalf("a refused tree check stored %q", entries)
	}
	if _, ok := f.remotes(t, f.globalPath)["ollama"]; ok {
		t.Fatal("a refused tree check must not register the record")
	}
}

// A target whose first segment names a record the file declares is not missing
// a declaration. Here the declared child is no-prefix, so it contributes no
// name segment and the name under it misses while the record exists — the
// refusal must not advise declaring what the file already declares.
func TestPullTreeCheckDanglingUnderDeclaredNoPrefixChild(t *testing.T) {
	f := newPullFixture(t)
	opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
		switch url {
		case "https://example.com/ollama.yaml":
			return []byte("profiles:\n  glm:\n    extends: [claude/default]\n    env:\n      MODEL: glm\nremotes:\n  claude:\n    url: https://example.com/claude.yaml\n    no_prefix: true\n"), "", nil
		case "https://example.com/claude.yaml":
			return []byte("profiles:\n  default:\n    env:\n      K: v\n"), "", nil
		}
		t.Errorf("unexpected fetch of %s", url)
		return nil, "", nil
	})

	_, err := Pull("https://example.com/ollama.yaml", "ollama", opts)
	if err == nil {
		t.Fatal("a dangling subtree target must fail the pull")
	}
	for _, want := range []string{`remote "ollama"`, `"ollama/claude/default"`, "no profile in the pulled subtree defines"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to carry %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "resolves only its own profiles") {
		t.Fatalf("err = %v, want no advice to declare a record the file already declares", err)
	}
	if entries := remoteFiles(t, f.remotesDir); len(entries) != 0 {
		t.Fatalf("a refused tree check stored %q", entries)
	}
}

func TestPullNestedNameNotCallable(t *testing.T) {
	f := newPullFixture(t)
	opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
		return serveNested(t, url, ref)
	})
	if _, err := Pull("https://example.com/ollama.yaml", "ollama", opts); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	before, _ := readOrMissing(t, config.RemoteContentPath("ollama/claude"))
	update := f.options(t, true, func(string, string) ([]byte, string, error) {
		t.Error("an update by a nested record path must not fetch")
		return nil, "", nil
	})

	_, err := PullUpdate("ollama/claude", update)
	if err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("err = %v, want the not-registered refusal", err)
	}
	if !strings.Contains(err.Error(), "enver pull ollama") {
		t.Fatalf("err = %v, want the hint to refresh through the parent", err)
	}
	if after, _ := readOrMissing(t, config.RemoteContentPath("ollama/claude")); after != before {
		t.Fatal("the refused update must leave the nested content untouched")
	}
}

// A no-prefix child under a no-prefix top-level record lands bare beside the
// parent's own bare names; the fold makes the later record win, and the pull
// must say which record that is by its path.
func TestPullNoPrefixTreeCollisions(t *testing.T) {
	f := newPullFixture(t)
	opts := f.options(t, true, func(url, ref string) ([]byte, string, error) {
		switch url {
		case "https://example.com/team.yaml":
			return []byte("profiles:\n  base:\n    env:\n      A: one\nremotes:\n  child:\n    url: https://example.com/child.yaml\n    no_prefix: true\n"), "", nil
		case "https://example.com/child.yaml":
			return []byte("profiles:\n  base:\n    env:\n      B: two\n"), "", nil
		}
		t.Errorf("unexpected fetch of %s", url)
		return nil, "", nil
	})
	opts.NoPrefix = true

	summary, err := Pull("https://example.com/team.yaml", "team", opts)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if _, ok := readOrMissing(t, config.RemoteContentPath("team/child")); !ok {
		t.Fatal("the no-prefix child was not stored under its parent's directory")
	}
	for _, want := range []string{
		"collides with the same profile name already loaded from remote",
		`"team/child" wins`,
		`remote "team/child"`,
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary misses %q:\n%s", want, summary)
		}
	}
	merged, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r, err := merged.ResolveProfile("base")
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}
	if r.Env["A"] != "one" || r.Env["B"] != "two" {
		t.Fatalf("env = %q, want both records' keys under the bare name", r.Env)
	}
}

// syncParentFirst declares one child; syncParentSecond replaces it with
// another, so one re-pull exercises the add and the remove together.
const (
	syncParentFirst = `profiles:
  base:
    env:
      HOST: first-upstream
remotes:
  first:
    url: https://example.com/first.yaml
`

	syncParentSecond = `profiles:
  base:
    env:
      HOST: second-upstream
remotes:
  second:
    url: https://example.com/second.yaml
`

	syncChildFirst = `profiles:
  one:
    env:
      K: v1
`

	syncChildSecond = `profiles:
  two:
    env:
      K: v2
`
)

// A re-pull lands the tree the upstream serves now: a child the declaration
// dropped is swept from disk, a child it added is stored, and both changes are
// reported, since the sweep is the only place stale entries leave.
func TestPullSyncAddsAndRemovesChildren(t *testing.T) {
	f := newPullFixture(t)
	fetches := map[string]int{}
	var parentContent string
	seam := func(url, _ string) ([]byte, string, error) {
		fetches[url]++
		switch url {
		case "https://example.com/tree.yaml":
			return []byte(parentContent), "root-commit", nil
		case "https://example.com/first.yaml":
			return []byte(syncChildFirst), "first-commit", nil
		case "https://example.com/second.yaml":
			return []byte(syncChildSecond), "second-commit", nil
		}
		t.Errorf("unexpected fetch of %s", url)
		return nil, "", nil
	}

	parentContent = syncParentFirst
	first, err := Pull("https://example.com/tree.yaml", "tree", f.options(t, true, seam))
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if !strings.Contains(first, "added") {
		t.Errorf("the first pull does not report the declared child as added:\n%s", first)
	}
	for path, want := range map[string]string{
		"tree":       syncParentFirst,
		"tree/first": syncChildFirst,
	} {
		content, ok := readOrMissing(t, config.RemoteContentPath(path))
		if !ok || content != want {
			t.Errorf("content for %s = %q ok=%v, want the fetched bytes verbatim", path, content, ok)
		}
	}

	parentContent = syncParentSecond
	summary, err := PullUpdate("tree", f.options(t, true, seam))
	if err != nil {
		t.Fatalf("PullUpdate: %v", err)
	}
	if fetches["https://example.com/first.yaml"] != 1 {
		t.Errorf("the dropped child was fetched %d times, want no fetch past the first pull", fetches["https://example.com/first.yaml"])
	}
	if fetches["https://example.com/second.yaml"] != 1 {
		t.Errorf("the added child was fetched %d times, want 1", fetches["https://example.com/second.yaml"])
	}
	if _, ok := readOrMissing(t, config.RemoteContentPath("tree/first")); ok {
		t.Error("the dropped child's content survived the re-pull")
	}
	if _, ok := readOrMissing(t, config.RemoteStatePath("tree/first")); ok {
		t.Error("the dropped child's sidecar survived the re-pull")
	}
	content, ok := readOrMissing(t, config.RemoteContentPath("tree/second"))
	if !ok || content != syncChildSecond {
		t.Errorf("content for tree/second = %q ok=%v, want the fetched bytes verbatim", content, ok)
	}
	st, ok := f.state(t, "tree/second")
	if !ok || st.Commit != "second-commit" {
		t.Errorf("state for tree/second = %+v ok=%v, want the fetch recorded", st, ok)
	}
	removedAt := strings.Index(summary, `removed stored content "tree/first"`)
	addedAt := strings.Index(summary, "added")
	childAt := strings.Index(summary, `✓ remote "tree/second"`)
	if removedAt < 0 || addedAt < 0 || childAt < 0 {
		t.Fatalf("summary misses the change lines:\n%s", summary)
	}
	if removedAt > childAt || addedAt < childAt {
		t.Errorf("the change lines render outside their owning blocks:\n%s", summary)
	}

	merged, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := merged.Profiles["tree/second/two"]; !ok {
		t.Error("the added child's profile does not resolve after the re-pull")
	}
	if _, ok := merged.Profiles["tree/first/one"]; ok {
		t.Error("the dropped child's profile still resolves after the re-pull")
	}
}

// A child whose stored copy the sidecar still vouches for is carried across a
// re-pull untouched: the fetch seam sees it exactly once across both pulls,
// and neither its content nor its sidecar is rewritten.
func TestPullSyncKeepsUnchangedChild(t *testing.T) {
	f := newPullFixture(t)
	fetches := map[string]int{}
	seam := func(url, ref string) ([]byte, string, error) {
		fetches[url]++
		return serveNested(t, url, ref)
	}

	if _, err := Pull("https://example.com/ollama.yaml", "ollama", f.options(t, true, seam)); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	contentPath := config.RemoteContentPath("ollama/claude")
	statePath := config.RemoteStatePath("ollama/claude")
	beforeContent, ok := readOrMissing(t, contentPath)
	if !ok {
		t.Fatal("the first pull did not store the child")
	}
	beforeContentFi, err := os.Stat(contentPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeStateFi, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeState, ok := f.state(t, "ollama/claude")
	if !ok {
		t.Fatal("the first pull did not write the child's sidecar")
	}

	summary, err := PullUpdate("ollama", f.options(t, true, seam))
	if err != nil {
		t.Fatalf("PullUpdate: %v", err)
	}
	if fetches["https://example.com/claude.yaml"] != 1 {
		t.Errorf("the child was fetched %d times across both pulls, want 1", fetches["https://example.com/claude.yaml"])
	}
	if fetches["https://example.com/ollama.yaml"] != 2 {
		t.Errorf("the pulled record was fetched %d times, want 2", fetches["https://example.com/ollama.yaml"])
	}
	afterContent, ok := readOrMissing(t, contentPath)
	if !ok || afterContent != beforeContent {
		t.Errorf("child content changed across the re-pull: %q", afterContent)
	}
	afterContentFi, err := os.Stat(contentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !afterContentFi.ModTime().Equal(beforeContentFi.ModTime()) {
		t.Error("the kept child's content file was rewritten")
	}
	afterStateFi, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !afterStateFi.ModTime().Equal(beforeStateFi.ModTime()) {
		t.Error("the kept child's sidecar was rewritten")
	}
	afterState, ok := f.state(t, "ollama/claude")
	if !ok || !afterState.PulledAt.Equal(beforeState.PulledAt) {
		t.Errorf("sidecar state changed across the re-pull: %+v -> %+v", beforeState, afterState)
	}
	for _, unwanted := range []string{"added", "re-pointed", "removed stored content"} {
		if strings.Contains(summary, unwanted) {
			t.Errorf("a kept child must not be reported %q:\n%s", unwanted, summary)
		}
	}
}

// A child re-pointed at another source is fetched fresh, and the summary names
// both sources, so the provenance flip is never silent.
func TestPullSyncRepointsChild(t *testing.T) {
	f := newPullFixture(t)
	parent := func(childURL string) []byte {
		return []byte(fmt.Sprintf(`profiles:
  glm-models:
    extends: claude/default
    env:
      MODEL: glm-4
remotes:
  claude:
    url: %s
`, childURL))
	}
	fetches := map[string]int{}
	var childURL string
	seam := func(url, _ string) ([]byte, string, error) {
		fetches[url]++
		switch url {
		case "https://example.com/ollama.yaml":
			return parent(childURL), "root-commit", nil
		case "https://example.com/claude-old.yaml", "https://example.com/claude.yaml":
			return []byte(nestedClaudeContent), "claude-commit", nil
		}
		t.Errorf("unexpected fetch of %s", url)
		return nil, "", nil
	}

	childURL = "https://example.com/claude-old.yaml"
	if _, err := Pull("https://example.com/ollama.yaml", "ollama", f.options(t, true, seam)); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	childURL = "https://example.com/claude.yaml"
	summary, err := PullUpdate("ollama", f.options(t, true, seam))
	if err != nil {
		t.Fatalf("PullUpdate: %v", err)
	}
	if fetches["https://example.com/claude-old.yaml"] != 1 {
		t.Errorf("the old source was fetched %d times, want once at the first pull", fetches["https://example.com/claude-old.yaml"])
	}
	if fetches["https://example.com/claude.yaml"] != 1 {
		t.Errorf("the new source was fetched %d times, want 1", fetches["https://example.com/claude.yaml"])
	}
	want := "re-pointed from https://example.com/claude-old.yaml to https://example.com/claude.yaml"
	if !strings.Contains(summary, want) {
		t.Errorf("summary misses the re-point line %q:\n%s", want, summary)
	}
	st, ok := f.state(t, "ollama/claude")
	if !ok || st.URL != "https://example.com/claude.yaml" {
		t.Errorf("state = %+v ok=%v, want the new source recorded", st, ok)
	}
	content, ok := readOrMissing(t, config.RemoteContentPath("ollama/claude"))
	if !ok || content != nestedClaudeContent {
		t.Errorf("content = %q ok=%v, want the freshly fetched bytes", content, ok)
	}
	merged, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r, err := merged.ResolveProfile("ollama/glm-models")
	if err != nil {
		t.Fatalf("resolve ollama/glm-models: %v", err)
	}
	if r.Env["API_BASE"] != "https://api.example.com" {
		t.Fatalf("env = %q, want the re-pointed child's key inherited", r.Env)
	}
}

// A grandchild a re-pointed child drops is swept too, and its removal reports
// on the child's own block: the nearest record the new tree still reaches owns
// the line.
func TestPullSyncSweepsDroppedGrandchild(t *testing.T) {
	f := newPullFixture(t)
	var midURL string
	seam := func(url, _ string) ([]byte, string, error) {
		switch url {
		case "https://example.com/tree.yaml":
			return []byte(fmt.Sprintf(`profiles:
  base:
    env:
      K: v
remotes:
  mid:
    url: %s
`, midURL)), "root-commit", nil
		case "https://example.com/mid-old.yaml":
			return []byte(`profiles:
  mid-p:
    env:
      K: v
remotes:
  leaf:
    url: https://example.com/leaf.yaml
`), "mid-commit", nil
		case "https://example.com/mid.yaml":
			return []byte("profiles:\n  mid-p:\n    env:\n      K: v\n"), "mid-commit", nil
		case "https://example.com/leaf.yaml":
			return []byte("profiles:\n  leaf-p:\n    env:\n      K: v\n"), "leaf-commit", nil
		}
		t.Errorf("unexpected fetch of %s", url)
		return nil, "", nil
	}

	midURL = "https://example.com/mid-old.yaml"
	if _, err := Pull("https://example.com/tree.yaml", "tree", f.options(t, true, seam)); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	midURL = "https://example.com/mid.yaml"
	summary, err := PullUpdate("tree", f.options(t, true, seam))
	if err != nil {
		t.Fatalf("PullUpdate: %v", err)
	}
	if _, ok := readOrMissing(t, config.RemoteContentPath("tree/mid/leaf")); ok {
		t.Fatal("the dropped grandchild's content survived the re-pull")
	}
	if _, ok := readOrMissing(t, config.RemoteStatePath("tree/mid/leaf")); ok {
		t.Fatal("the dropped grandchild's sidecar survived the re-pull")
	}
	if _, ok := readOrMissing(t, config.RemoteContentPath("tree/mid")); !ok {
		t.Fatal("the re-pointed child itself must stay stored")
	}
	removedAt := strings.Index(summary, `removed stored content "tree/mid/leaf"`)
	midAt := strings.Index(summary, `✓ remote "tree/mid"`)
	if removedAt < 0 || midAt < 0 || removedAt < midAt {
		t.Fatalf("the grandchild's removal does not report inside the child's block:\n%s", summary)
	}
	if !strings.Contains(summary, "re-pointed from https://example.com/mid-old.yaml to https://example.com/mid.yaml") {
		t.Errorf("summary misses the child's re-point line:\n%s", summary)
	}
}

// An upstream rename of a child key that changes only its spelling must not
// leave the keep decision and the sweep answering the question "is this child
// stored?" differently: on a filesystem that folds name case, the stored copy
// opens under either spelling while the sweep compares byte-exact, so one pull
// would keep the child and then delete it. A case-only mismatch is a rename:
// the child is fetched fresh under the declared spelling, the old spelling
// leaves the disk, and the next pull keeps the child at the new one. The probe
// skips filesystems that tell names apart by case, where the mismatch cannot
// arise.
func TestPullSyncRefetchesCaseRenamedChild(t *testing.T) {
	probeDir := t.TempDir()
	probe := filepath.Join(probeDir, "Foo")
	if err := os.WriteFile(probe, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(probeDir, "foo")); err != nil {
		t.Skip("the filesystem tells file names apart by case, so a case-only rename cannot happen here")
	}

	f := newPullFixture(t)
	parent := func(child string) []byte {
		return []byte(fmt.Sprintf(`profiles:
  glm-models:
    extends: %s/default
    env:
      MODEL: glm-4
remotes:
  %s:
    url: https://example.com/claude.yaml
`, child, child))
	}
	fetches := map[string]int{}
	var childKey string
	seam := func(url, _ string) ([]byte, string, error) {
		fetches[url]++
		switch url {
		case "https://example.com/ollama.yaml":
			return parent(childKey), "ollama-commit", nil
		case "https://example.com/claude.yaml":
			return []byte(nestedClaudeContent), "claude-commit", nil
		}
		t.Errorf("unexpected fetch of %s", url)
		return nil, "", nil
	}

	childKey = "Claude"
	if _, err := Pull("https://example.com/ollama.yaml", "ollama", f.options(t, true, seam)); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if _, ok := readOrMissing(t, config.RemoteContentPath("ollama/Claude")); !ok {
		t.Fatal("the first pull did not store the child")
	}

	childKey = "claude"
	summary, err := PullUpdate("ollama", f.options(t, true, seam))
	if err != nil {
		t.Fatalf("PullUpdate: %v", err)
	}
	if fetches["https://example.com/claude.yaml"] != 2 {
		t.Errorf("the renamed child was fetched %d times, want a fresh fetch at the re-pull", fetches["https://example.com/claude.yaml"])
	}
	want := `renamed from "ollama/Claude"`
	if !strings.Contains(summary, want) {
		t.Errorf("summary misses the rename line %q:\n%s", want, summary)
	}
	entries, err := os.ReadDir(filepath.Join(f.remotesDir, "ollama"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"claude.meta", "claude.yaml"}) {
		t.Errorf("the record directory holds %q, want the declared spelling only", names)
	}
	content, ok := readOrMissing(t, config.RemoteContentPath("ollama/claude"))
	if !ok || content != nestedClaudeContent {
		t.Errorf("content for ollama/claude = %q ok=%v, want the fetched bytes under the declared spelling", content, ok)
	}
	if _, ok := f.state(t, "ollama/claude"); !ok {
		t.Error("the re-pull did not write the child's sidecar under the declared spelling")
	}

	if _, err := PullUpdate("ollama", f.options(t, true, seam)); err != nil {
		t.Fatalf("second PullUpdate: %v", err)
	}
	if fetches["https://example.com/claude.yaml"] != 2 {
		t.Errorf("the child was fetched %d times; a case-only rename must not loop", fetches["https://example.com/claude.yaml"])
	}

	merged, err := Load(Options{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r, err := merged.ResolveProfile("ollama/glm-models")
	if err != nil {
		t.Fatalf("resolve ollama/glm-models: %v", err)
	}
	if r.Env["API_BASE"] != "https://api.example.com" {
		t.Fatalf("env = %q, want the renamed child's key inherited", r.Env)
	}
}
