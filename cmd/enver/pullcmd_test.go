package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/remote"
	"github.com/spf13/cobra"
)

// pullContent is a minimal valid remote: two profiles, one extending the other
// inside the same file.
const pullContent = `profiles:
  ollama:
    env:
      OLLAMA_HOST: http://localhost:11434
  qwen:
    extends: ollama
    env:
      MODEL: qwen2.5-coder
`

// pullCmdFixture isolates the global config, the local .enver.yaml, and the
// remotes directory, and points globalFlags at the isolated global file.
type pullCmdFixture struct {
	globalPath string
	localPath  string
	remotesDir string
}

func newPullCmdFixture(t *testing.T) *pullCmdFixture {
	t.Helper()
	dir := chdirTemp(t)
	saveGlobalFlags(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	remotes := t.TempDir()
	prevDir := config.RemotesDir
	config.RemotesDir = func() string { return remotes }
	t.Cleanup(func() { config.RemotesDir = prevDir })

	globalFlags.configPath = filepath.Join(dir, "config.yaml")
	globalFlags.noLocal = false
	globalFlags.global = false

	// Fixtures below create divergent views on purpose; keep their advisories out
	// of the test log unless a test captures the sink.
	prevOut := loadWarningsOut
	loadWarningsOut = io.Discard
	t.Cleanup(func() { loadWarningsOut = prevOut })

	return &pullCmdFixture{
		globalPath: globalFlags.configPath,
		localPath:  config.LocalPath(),
		remotesDir: remotes,
	}
}

// withPullFlags snapshots the pull globals, restores them on cleanup, and
// installs an injected fetch that serves pullContent.
func withPullFlags(t *testing.T, fetch func(url, ref string) ([]byte, string, error)) {
	t.Helper()
	prevAs, prevLocal, prevNoPrefix, prevRm, prevForce, prevList := pullAs, pullLocal, pullNoPrefix, pullRm, pullForce, pullList
	prevFetch := pullFetch
	t.Cleanup(func() {
		pullAs, pullLocal, pullNoPrefix, pullRm, pullForce, pullList = prevAs, prevLocal, prevNoPrefix, prevRm, prevForce, prevList
		pullFetch = prevFetch
	})
	pullAs, pullLocal, pullNoPrefix, pullRm, pullForce, pullList = "", false, false, false, false, false
	if fetch == nil {
		fetch = func(string, string) ([]byte, string, error) {
			t.Error("no fetch expected on this path")
			return nil, "", errors.New("unexpected fetch")
		}
	}
	pullFetch = fetch
}

func writePullFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// runPull invokes the command the way a test can: RunE directly, so inherited
// persistent flags are read from globalFlags instead of being parsed.
func runPull(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := pullCmd.RunE(cmd, args)
	return out.String(), err
}

func pullRemotes(t *testing.T, path string) map[string]config.RemoteRecord {
	t.Helper()
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	return cfg.Remotes
}

func TestPullCmdRegistersRemote(t *testing.T) {
	setNonInteractive(t)
	f := newPullCmdFixture(t)
	var fetchedURL, fetchedRef string
	withPullFlags(t, func(url, ref string) ([]byte, string, error) {
		fetchedURL, fetchedRef = url, ref
		return []byte(pullContent), "9f2c1ab", nil
	})
	pullAs = "llm"

	out, err := runPull(t, "github:someone/llm-profiles@v2")
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if want := "https://raw.githubusercontent.com/someone/llm-profiles/v2/enver.yaml"; fetchedURL != want {
		t.Fatalf("fetched %q, want %q", fetchedURL, want)
	}
	if fetchedRef != "v2" {
		t.Fatalf("fetched ref %q, want v2", fetchedRef)
	}
	for _, want := range []string{`remote "llm"`, "github:someone/llm-profiles", "llm/ollama", "llm/qwen"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary misses %q:\n%s", want, out)
		}
	}
	rec, ok := pullRemotes(t, f.globalPath)["llm"]
	if !ok {
		t.Fatalf("record not registered in the global config %s", f.globalPath)
	}
	if rec.URL != "github:someone/llm-profiles" || rec.Ref != "v2" {
		t.Fatalf("record = %+v, want the source spelling with its ref split out", rec)
	}
	if _, ok := pullRemotes(t, f.localPath)["llm"]; ok {
		t.Fatal("registration must default to the global layer")
	}
	if _, err := os.Stat(config.RemoteContentPath("llm")); err != nil {
		t.Fatalf("content not stored: %v", err)
	}
	if st, ok := config.ReadRemoteState("llm"); !ok || st.Commit != "9f2c1ab" {
		t.Fatalf("sidecar = %+v ok=%v, want the fetched commit recorded", st, ok)
	}
}

func TestPullCmdLocalWritesProjectFile(t *testing.T) {
	setNonInteractive(t)
	f := newPullCmdFixture(t)
	withPullFlags(t, func(string, string) ([]byte, string, error) {
		return []byte(pullContent), "", nil
	})
	pullAs = "llm"
	pullLocal = true

	if _, err := runPull(t, "https://example.com/llm.yaml"); err != nil {
		t.Fatalf("pull --local: %v", err)
	}
	if _, ok := pullRemotes(t, f.localPath)["llm"]; !ok {
		t.Fatalf("record not written to %s", f.localPath)
	}
	if _, ok := pullRemotes(t, f.globalPath)["llm"]; ok {
		t.Fatal("--local must not write the global config")
	}
}

func TestPullCmdRemoveBothLayers(t *testing.T) {
	setNonInteractive(t)
	both := func(t *testing.T) *pullCmdFixture {
		f := newPullCmdFixture(t)
		writePullFile(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/global.yaml\n")
		writePullFile(t, f.localPath, "remotes:\n  llm:\n    url: https://example.com/local.yaml\n")
		if err := config.WriteRemoteContent("llm", []byte(pullContent)); err != nil {
			t.Fatal(err)
		}
		return f
	}

	t.Run("closest declaration without a layer flag", func(t *testing.T) {
		f := both(t)
		withPullFlags(t, nil)
		pullRm = true

		out, err := runPull(t, "llm")
		if err != nil {
			t.Fatalf("pull --rm: %v", err)
		}
		if _, ok := pullRemotes(t, f.localPath)["llm"]; ok {
			t.Fatal("the effective (local) declaration must be removed")
		}
		if _, ok := pullRemotes(t, f.globalPath)["llm"]; !ok {
			t.Fatal("the global declaration must survive")
		}
		if _, err := os.Stat(config.RemoteContentPath("llm")); err != nil {
			t.Fatalf("content must survive a surviving declaration: %v", err)
		}
		for _, want := range []string{`remote "llm"`, "global", "https://example.com/global.yaml", "enver pull llm"} {
			if !strings.Contains(out, want) {
				t.Errorf("summary misses %q:\n%s", want, out)
			}
		}
	})

	t.Run("explicit global layer", func(t *testing.T) {
		f := both(t)
		withPullFlags(t, nil)
		pullRm = true
		globalFlags.global = true

		out, err := runPull(t, "llm")
		if err != nil {
			t.Fatalf("pull --rm -g: %v", err)
		}
		if _, ok := pullRemotes(t, f.globalPath)["llm"]; ok {
			t.Fatal("the pinned global declaration must be removed")
		}
		if _, ok := pullRemotes(t, f.localPath)["llm"]; !ok {
			t.Fatal("an explicit layer must leave the other layer alone")
		}
		if _, err := os.Stat(config.RemoteContentPath("llm")); err != nil {
			t.Fatalf("content must survive the remaining local declaration: %v", err)
		}
		if !strings.Contains(out, "local") {
			t.Errorf("summary should name the surviving layer:\n%s", out)
		}
	})

	t.Run("explicit local layer", func(t *testing.T) {
		f := both(t)
		withPullFlags(t, nil)
		pullRm = true
		pullLocal = true

		if _, err := runPull(t, "llm"); err != nil {
			t.Fatalf("pull --rm --local: %v", err)
		}
		if _, ok := pullRemotes(t, f.localPath)["llm"]; ok {
			t.Fatal("the pinned local declaration must be removed")
		}
		if _, ok := pullRemotes(t, f.globalPath)["llm"]; !ok {
			t.Fatal("an explicit layer must leave the other layer alone")
		}
	})

	t.Run("last declaration deletes content", func(t *testing.T) {
		f := newPullCmdFixture(t)
		writePullFile(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/global.yaml\n")
		writePullFile(t, f.localPath, "profiles:\n  llm/ollama:\n    env:\n      OLLAMA_API_KEY: sk-live\n")
		if err := config.WriteRemoteContent("llm", []byte(pullContent)); err != nil {
			t.Fatal(err)
		}
		if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/global.yaml"}); err != nil {
			t.Fatal(err)
		}
		withPullFlags(t, nil)
		pullRm = true

		out, err := runPull(t, "llm")
		if err != nil {
			t.Fatalf("pull --rm: %v", err)
		}
		if _, err := os.Stat(config.RemoteContentPath("llm")); !os.IsNotExist(err) {
			t.Fatalf("content must be deleted with the last declaration: %v", err)
		}
		if _, err := os.Stat(config.RemoteStatePath("llm")); !os.IsNotExist(err) {
			t.Fatalf("sidecar must be deleted with the content: %v", err)
		}
		for _, want := range []string{"llm/ollama", "no longer inherits"} {
			if !strings.Contains(out, want) {
				t.Errorf("summary misses the orphaned patch note %q:\n%s", want, out)
			}
		}
	})
}

func TestPullCmdRejectsRmConflicts(t *testing.T) {
	setNonInteractive(t)
	f := newPullCmdFixture(t)
	writePullFile(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/global.yaml\n")
	writePullFile(t, f.localPath, "remotes:\n  llm:\n    url: https://example.com/local.yaml\n")
	withPullFlags(t, nil)
	pullRm = true

	pullLocal, globalFlags.global = true, true
	if _, err := runPull(t, "llm"); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("err = %v, want --rm --local -g to be rejected like any other pull", err)
	}

	pullLocal, globalFlags.global = false, false
	pullAs = "other"
	if _, err := runPull(t, "llm"); err == nil || !strings.Contains(err.Error(), "--as") {
		t.Fatalf("err = %v, want --rm to reject --as", err)
	}
	pullAs = ""

	pullNoPrefix = true
	if _, err := runPull(t, "llm"); err == nil || !strings.Contains(err.Error(), "--no-prefix") {
		t.Fatalf("err = %v, want --rm to reject --no-prefix", err)
	}
	pullNoPrefix = false

	pullForce = true
	if _, err := runPull(t, "llm"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want --rm to reject --force", err)
	}

	for _, path := range []string{f.globalPath, f.localPath} {
		if _, ok := pullRemotes(t, path)["llm"]; !ok {
			t.Fatalf("a rejected --rm must not remove the declaration in %s", path)
		}
	}
}

// A removal must see every declaration, --no-local included: deleting the
// content a project file still declares would blank its profiles.
func TestPullCmdRemoveNoLocalSeesTheProjectFile(t *testing.T) {
	setNonInteractive(t)
	f := newPullCmdFixture(t)
	writePullFile(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/global.yaml\n")
	writePullFile(t, f.localPath, "remotes:\n  llm:\n    url: https://example.com/local.yaml\n")
	if err := config.WriteRemoteContent("llm", []byte(pullContent)); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/local.yaml"}); err != nil {
		t.Fatal(err)
	}
	withPullFlags(t, nil)
	pullRm = true
	globalFlags.noLocal = true
	globalFlags.global = true

	out, err := runPull(t, "llm")
	if err != nil {
		t.Fatalf("pull --rm --no-local -g: %v", err)
	}
	if _, ok := pullRemotes(t, f.globalPath)["llm"]; ok {
		t.Fatal("the pinned global declaration must be removed")
	}
	if _, ok := pullRemotes(t, f.localPath)["llm"]; !ok {
		t.Fatal("the local declaration must survive --no-local")
	}
	if _, err := os.Stat(config.RemoteContentPath("llm")); err != nil {
		t.Fatalf("content must survive the surviving local declaration: %v", err)
	}
	if _, err := os.Stat(config.RemoteStatePath("llm")); err != nil {
		t.Fatalf("sidecar must survive the surviving local declaration: %v", err)
	}
	if !strings.Contains(out, "local") {
		t.Errorf("summary should name the surviving layer:\n%s", out)
	}
}

// --no-local means the global scope: the removal targets the global declaration,
// while the project file stays visible for the content decision.
func TestPullCmdRemoveNoLocalTargetsGlobal(t *testing.T) {
	setNonInteractive(t)
	f := newPullCmdFixture(t)
	writePullFile(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/global.yaml\n")
	writePullFile(t, f.localPath, "remotes:\n  llm:\n    url: https://example.com/local.yaml\n")
	if err := config.WriteRemoteContent("llm", []byte(pullContent)); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/local.yaml"}); err != nil {
		t.Fatal(err)
	}
	withPullFlags(t, nil)
	pullRm = true
	globalFlags.noLocal = true

	out, err := runPull(t, "llm")
	if err != nil {
		t.Fatalf("pull --rm --no-local: %v", err)
	}
	if _, ok := pullRemotes(t, f.globalPath)["llm"]; ok {
		t.Fatal("--no-local targets the global declaration")
	}
	if _, ok := pullRemotes(t, f.localPath)["llm"]; !ok {
		t.Fatal("the project file's declaration is out of view and must survive")
	}
	if _, err := os.Stat(config.RemoteContentPath("llm")); err != nil {
		t.Fatalf("content must survive a declaration the hidden layer still needs: %v", err)
	}
	if _, err := os.Stat(config.RemoteStatePath("llm")); err != nil {
		t.Fatalf("sidecar must survive a declaration the hidden layer still needs: %v", err)
	}
	if !strings.Contains(out, "still declared in the local") {
		t.Errorf("summary should name the surviving local declaration:\n%s", out)
	}
}

func TestPullCmdRemoveNeedsName(t *testing.T) {
	setNonInteractive(t)
	newPullCmdFixture(t)
	withPullFlags(t, nil)
	pullRm = true
	if _, err := runPull(t); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("err = %v, want --rm to require a remote name", err)
	}
}

func TestPullCmdTakeoverNeedsForceWhenNonInteractive(t *testing.T) {
	setNonInteractive(t)
	f := newPullCmdFixture(t)
	writePullFile(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/old.yaml\n")
	writePullFile(t, f.localPath, "profiles:\n  llm/ollama:\n    env:\n      OLLAMA_API_KEY: sk-live\n")
	if err := config.WriteRemoteContent("llm", []byte(pullContent)); err != nil {
		t.Fatal(err)
	}
	withPullFlags(t, func(string, string) ([]byte, string, error) {
		return []byte(pullContent), "", nil
	})
	pullAs = "llm"

	_, err := runPull(t, "https://example.com/new.yaml")
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a non-zero error naming --force", err)
	}
	if rec := pullRemotes(t, f.globalPath)["llm"]; rec.URL != "https://example.com/old.yaml" {
		t.Fatalf("record = %+v, want no write without confirmation", rec)
	}

	// --force is the non-interactive escape and still reports the takeover.
	pullForce = true
	out, err := runPull(t, "https://example.com/new.yaml")
	if err != nil {
		t.Fatalf("pull --force: %v", err)
	}
	if !strings.Contains(out, "will now compose with content from") {
		t.Errorf("forced pull must still warn about the takeover:\n%s", out)
	}
	if rec := pullRemotes(t, f.globalPath)["llm"]; rec.URL != "https://example.com/new.yaml" {
		t.Fatalf("record = %+v, want re-pointed under --force", rec)
	}
}

func TestPullCmdRegistrationNeedsAs(t *testing.T) {
	setNonInteractive(t)
	newPullCmdFixture(t)
	withPullFlags(t, nil)

	if _, err := runPull(t, "https://example.com/llm.yaml"); err == nil || !strings.Contains(err.Error(), "--as") {
		t.Fatalf("err = %v, want registration to require --as", err)
	}
	if _, err := runPull(t, "not a source"); err == nil || !strings.Contains(err.Error(), "remote name") {
		t.Fatalf("err = %v, want a bare argument that is neither a source nor a registered name to fail", err)
	}
}

func TestPullCmdRefetchesByName(t *testing.T) {
	setNonInteractive(t)
	f := newPullCmdFixture(t)
	writePullFile(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/llm.yaml\n    ref: v2\n")
	withPullFlags(t, func(url, ref string) ([]byte, string, error) {
		return []byte(pullContent), "", nil
	})

	out, err := runPull(t, "llm")
	if err != nil {
		t.Fatalf("pull llm: %v", err)
	}
	if !strings.Contains(out, `remote "llm"`) {
		t.Errorf("summary misses the record:\n%s", out)
	}
	if st, ok := config.ReadRemoteState("llm"); !ok || st.URL != "https://example.com/llm.yaml" || st.Ref != "v2" {
		t.Fatalf("sidecar = %+v ok=%v, want the record's source", st, ok)
	}
	if rec := pullRemotes(t, f.globalPath)["llm"]; rec.Ref != "v2" {
		t.Fatalf("record = %+v, want the ref untouched by a re-fetch", rec)
	}
	// --as names a new registration; it cannot rename an existing record.
	pullAs = "other"
	if _, err := runPull(t, "llm"); err == nil || !strings.Contains(err.Error(), "--as") {
		t.Fatalf("err = %v, want --as refused on a re-fetch", err)
	}
}

func TestPullCmdWithoutArgumentsUpdatesAll(t *testing.T) {
	setNonInteractive(t)
	f := newPullCmdFixture(t)
	writePullFile(t, f.globalPath, "remotes:\n  one:\n    url: https://example.com/one.yaml\n  two:\n    url: https://example.com/two.yaml\n")
	withPullFlags(t, func(string, string) ([]byte, string, error) {
		return []byte(pullContent), "", nil
	})

	out, err := runPull(t)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	for _, want := range []string{`remote "one"`, `remote "two"`} {
		if !strings.Contains(out, want) {
			t.Errorf("summary misses %q:\n%s", want, out)
		}
	}
	for _, name := range []string{"one", "two"} {
		if _, ok := config.ReadRemoteState(name); !ok {
			t.Fatalf("%s was not pulled", name)
		}
	}
	if rec := pullRemotes(t, f.globalPath)["one"]; rec.URL != "https://example.com/one.yaml" {
		t.Fatalf("record = %+v, want an unchanged record", rec)
	}
}

func TestPullCmdCompletesRecordNames(t *testing.T) {
	f := newPullCmdFixture(t)
	writePullFile(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/llm.yaml\n  team-base:\n    url: https://example.com/team.yaml\n")

	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")
	cmd.Flags().Bool("no-local", false, "")
	_ = cmd.Flags().Set("config", f.globalPath)

	got, directive := completePull(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive = %v, want NoFileComp", directive)
	}
	want := []string{"llm", "team-base"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("completions = %v, want %v", got, want)
	}
	if got, _ := completePull(cmd, []string{"llm"}, ""); got != nil {
		t.Fatalf("completions past arg 0 = %v, want none", got)
	}
}

// A remote that cannot be fetched keeps the old content and the record, and the
// command fails: a pull never half-lands.
func TestPullCmdFetchFailureKeepsEverything(t *testing.T) {
	setNonInteractive(t)
	f := newPullCmdFixture(t)
	writePullFile(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/llm.yaml\n")
	if err := config.WriteRemoteContent("llm", []byte(pullContent)); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/llm.yaml"}); err != nil {
		t.Fatal(err)
	}
	withPullFlags(t, func(string, string) ([]byte, string, error) {
		return nil, "", errors.New("connection reset")
	})

	if _, err := runPull(t, "llm"); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("err = %v, want the fetch failure", err)
	}
	content, err := os.ReadFile(config.RemoteContentPath("llm"))
	if err != nil || string(content) != pullContent {
		t.Fatalf("content = %q, %v; want the previous bytes", content, err)
	}
	if st, ok := config.ReadRemoteState("llm"); !ok || st.URL != "https://example.com/llm.yaml" {
		t.Fatalf("sidecar = %+v ok=%v, want the previous state", st, ok)
	}
	// remote.ErrDowngrade must not be how a plain-http registration fails: that
	// is a source-grammar error, and conflating the two would hide a real
	// redirect downgrade.
	pullAs = "http"
	_, err = runPull(t, "http://example.com/llm.yaml")
	if err == nil || errors.Is(err, remote.ErrDowngrade) {
		t.Fatalf("err = %v, want a plain source error, not ErrDowngrade", err)
	}
}

// A project that re-declares a global record with its own source makes a plain
// re-fetch a takeover: the stored content came from the global source and a
// global patch profile sits under the prefix, so a non-interactive run must
// refuse before fetching and name --force.
func TestPullCmdRefetchTakeoverNeedsForceWhenNonInteractive(t *testing.T) {
	setNonInteractive(t)
	f := newPullCmdFixture(t)
	writePullFile(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/global.yaml\nprofiles:\n  llm/secret:\n    extends: llm/base\n    env:\n      API_KEY: sk-live\n")
	writePullFile(t, f.localPath, "remotes:\n  llm:\n    url: https://example.com/project.yaml\n")
	if err := config.WriteRemoteContent("llm", []byte(pullContent)); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/global.yaml"}); err != nil {
		t.Fatal(err)
	}
	var fetches int
	withPullFlags(t, func(string, string) ([]byte, string, error) {
		fetches++
		return []byte(pullContent), "bbbb222", nil
	})

	_, err := runPull(t, "llm")
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v, want a non-zero error naming --force", err)
	}
	if fetches != 0 {
		t.Fatalf("fetches = %d, want none before the takeover is accepted", fetches)
	}

	pullForce = true
	out, err := runPull(t, "llm")
	if err != nil {
		t.Fatalf("pull --force llm: %v", err)
	}
	if fetches != 1 {
		t.Fatalf("fetches = %d, want the forced fetch", fetches)
	}
	for _, want := range []string{"will now compose with content from", "stored content came from https://example.com/global.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("forced re-fetch misses %q:\n%s", want, out)
		}
	}
}

// enver pull --list is the freshness view: each record's source, whether it is
// pinned, and what the sidecar recorded for the last pull. It fetches nothing
// and writes nothing, and the unpinned marker carries the commit an unpinned
// record is actually held to.
func TestPullCmdListsFreshness(t *testing.T) {
	setNonInteractive(t)
	f := newPullCmdFixture(t)
	pulledAt := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	prevNow := pullListNow
	t.Cleanup(func() { pullListNow = prevNow })
	pullListNow = func() time.Time { return pulledAt.Add(3 * time.Hour) }

	writePullFile(t, f.globalPath, "remotes:\n  team:\n    url: https://example.com/team.yaml\n  llm:\n    url: github:someone/llm-profiles\n")
	if err := config.WriteRemoteState("llm", config.RemoteState{
		URL:      "https://raw.githubusercontent.com/someone/llm-profiles/HEAD/enver.yaml",
		Ref:      "HEAD",
		Commit:   "9f2c1ab",
		PulledAt: pulledAt,
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(f.globalPath)
	if err != nil {
		t.Fatal(err)
	}
	withPullFlags(t, nil) // a listing must not fetch
	pullList = true

	out, err := runPull(t)
	if err != nil {
		t.Fatalf("pull --list: %v", err)
	}
	for _, want := range []string{
		"# remotes: 2",
		"llm  github:someone/llm-profiles",
		"unpinned",
		"last pulled commit 9f2c1ab",
		"pulled 2026-11-15 09:00 UTC (3h ago)",
		"team  https://example.com/team.yaml",
		"never pulled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("listing misses %q:\n%s", want, out)
		}
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("a redirected listing carries ANSI escapes: %q", out)
	}
	if after, err := os.ReadFile(f.globalPath); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("a listing must not rewrite the config: %v", err)
	}
	if _, err := os.Stat(config.RemoteContentPath("team")); !os.IsNotExist(err) {
		t.Fatalf("a listing must not fetch: team content = %v", err)
	}
}

// An empty registry says so instead of printing a bare header.
func TestPullCmdListEmpty(t *testing.T) {
	setNonInteractive(t)
	newPullCmdFixture(t)
	withPullFlags(t, nil)
	pullList = true

	out, err := runPull(t)
	if err != nil {
		t.Fatalf("pull --list: %v", err)
	}
	if !strings.Contains(out, "no remotes registered") {
		t.Fatalf("empty listing misses the hint:\n%s", out)
	}
}

// The listing is a view, not an action: every flag that changes the registry is
// refused instead of silently ignored.
func TestPullCmdListRejectsChangingFlags(t *testing.T) {
	setNonInteractive(t)
	newPullCmdFixture(t)
	withPullFlags(t, nil)
	pullList = true

	if _, err := runPull(t, "llm"); err == nil || !strings.Contains(err.Error(), "no source") {
		t.Fatalf("err = %v, want --list to take no argument", err)
	}
	for _, tc := range []struct {
		flag  string
		set   func()
		clear func()
	}{
		{"--as", func() { pullAs = "llm" }, func() { pullAs = "" }},
		{"--no-prefix", func() { pullNoPrefix = true }, func() { pullNoPrefix = false }},
		{"--rm", func() { pullRm = true }, func() { pullRm = false }},
		{"--force", func() { pullForce = true }, func() { pullForce = false }},
	} {
		tc.set()
		if _, err := runPull(t); err == nil || !strings.Contains(err.Error(), tc.flag) {
			t.Fatalf("err = %v, want --list to refuse %s", err, tc.flag)
		}
		tc.clear()
	}
	pullLocal = true
	if _, err := runPull(t); err == nil || !strings.Contains(err.Error(), "--local") {
		t.Fatalf("err = %v, want --list to refuse --local", err)
	}
}

// TestPrintRemoteStatusesColor pins the styling contract: the record name is the
// key and renders bold, the source stays plain, and the freshness note renders
// dim.
func TestPrintRemoteStatusesColor(t *testing.T) {
	const (
		ansiBold  = "\x1b[1m"
		ansiDim   = "\x1b[2m"
		ansiReset = "\x1b[m"
	)
	pulledAt := time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)
	prevNow := pullListNow
	t.Cleanup(func() { pullListNow = prevNow })
	pullListNow = func() time.Time { return pulledAt.Add(3 * time.Hour) }
	rows := []app.RemoteStatus{{
		Name:     "llm",
		Source:   "github:someone/llm-profiles",
		Pulled:   true,
		Commit:   "9f2c1ab",
		PulledAt: pulledAt,
	}}

	var plain bytes.Buffer
	if err := printRemoteStatuses(&plain, rows); err != nil {
		t.Fatalf("printRemoteStatuses: %v", err)
	}
	if strings.ContainsRune(plain.String(), '\x1b') {
		t.Fatalf("a non-terminal writer got styling: %q", plain.String())
	}
	if !strings.Contains(plain.String(), "llm  github:someone/llm-profiles") {
		t.Fatalf("plain listing lost the row: %q", plain.String())
	}

	orig := writerIsTTY
	t.Cleanup(func() { writerIsTTY = orig })
	writerIsTTY = func(io.Writer) bool { return true }
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	colored := filepath.Join(t.TempDir(), "listing.txt")
	if err := os.WriteFile(colored, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(colored, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := printRemoteStatuses(f, rows); err != nil {
		t.Fatalf("printRemoteStatuses: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(colored)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"name":   ansiBold + "llm" + ansiReset,
		"note":   ansiDim + "# unpinned · last pulled commit 9f2c1ab",
		"source": "  github:someone/llm-profiles  ",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("colored listing missing %s: want %q in %q", name, want, out)
		}
	}
}

// A tree listing indents each child row under its parent and keeps the exact
// columns a flat listing renders, so nested records read as the tree a pull
// stores.
func TestPrintRemoteStatusesIndentsTree(t *testing.T) {
	rows := []app.RemoteStatus{
		{Name: "tree", Source: "https://example.com/tree.yaml", Depth: 1},
		{Name: "tree/alpha", Source: "https://example.com/alpha.yaml", Depth: 2},
		{Name: "tree/alpha/deep", Source: "https://example.com/deep.yaml", Depth: 3},
	}

	var plain bytes.Buffer
	if err := printRemoteStatuses(&plain, rows); err != nil {
		t.Fatalf("printRemoteStatuses: %v", err)
	}
	want := `# remotes: 3
tree  https://example.com/tree.yaml  # unpinned · never pulled
  tree/alpha  https://example.com/alpha.yaml  # unpinned · never pulled
    tree/alpha/deep  https://example.com/deep.yaml  # unpinned · never pulled
`
	if plain.String() != want {
		t.Fatalf("listing =\n%q\nwant\n%q", plain.String(), want)
	}
}
