package app

import (
	"errors"
	"os"
	"path/filepath"
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

// The gate classifies a passthrough target through the merged view, so a remote
// that extends a profile belonging to another remote is told exactly that.
func TestPullGateSeesOtherRemotes(t *testing.T) {
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
	if layer := opts.Merged.ProfileLayer["other/base"]; layer != config.RemoteLayer("other") {
		t.Fatalf("ProfileLayer[other/base] = %q, want the other remote's layer; the gate can only classify what the merged view shows", layer)
	}

	summary, err := Pull("https://example.com/svc.yaml", "svc", opts)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	for _, want := range []string{`"other/base"`, `it binds to another remote, "other"`} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary misses %q:\n%s", want, summary)
		}
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

func TestPullSummaryListsPassthroughTargets(t *testing.T) {
	f := newPullFixture(t)
	f.write(t, f.localPath, "profiles:\n  provider/base:\n    env:\n      HOST: local\n")
	opts := f.options(t, true, func(string, string) ([]byte, string, error) {
		return []byte("profiles:\n  svc:\n    extends: provider/base\n    env:\n      K: v\n"), "", nil
	})
	summary, err := Pull("https://example.com/svc.yaml", "svc", opts)
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	for _, want := range []string{`"provider/base"`, "does not define", "binds to your local profile"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary misses the passthrough note %q:\n%s", want, summary)
		}
	}
}
