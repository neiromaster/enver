package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

// Load-time advisories Config.LoadMerged collects — a diverged remote, orphaned
// content, a no-prefix fold collision — reach the user on stderr from the shared
// load path, never on stdout, and never through completion.

const loadWarningsBody = "profiles:\n  dev:\n    env:\n      A: \"1\"\n"

// divergentConfig builds a config whose one record points at a source its stored
// content was not fetched from, which the load reports as a warning.
func divergentConfig(t *testing.T) *pullCmdFixture {
	t.Helper()
	f := newPullCmdFixture(t)
	writePullFile(t, f.globalPath, "remotes:\n  llm:\n    url: https://example.com/new.yaml\n"+loadWarningsBody)
	if err := config.WriteRemoteContent("llm", []byte("profiles:\n  base:\n    env:\n      K: pulled\n")); err != nil {
		t.Fatal(err)
	}
	if err := config.WriteRemoteState("llm", config.RemoteState{URL: "https://example.com/old.yaml"}); err != nil {
		t.Fatal(err)
	}
	globalFlags.noLocal = true
	return f
}

// captureLoadWarnings swaps the warning sink for a buffer.
func captureLoadWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := loadWarningsOut
	loadWarningsOut = &buf
	t.Cleanup(func() { loadWarningsOut = prev })
	return &buf
}

func TestLoadWarningsRenderOnReadCommands(t *testing.T) {
	commands := map[string]func(out io.Writer) error{
		"show": func(out io.Writer) error {
			cmd := &cobra.Command{}
			cmd.SetOut(out)
			return showCmd.RunE(cmd, []string{"dev"})
		},
		"list": func(out io.Writer) error {
			cmd := &cobra.Command{}
			cmd.SetOut(out)
			return listCmd.RunE(cmd, nil)
		},
		"validate": func(out io.Writer) error {
			cmd := &cobra.Command{}
			cmd.SetOut(out)
			return validateCmd.RunE(cmd, nil)
		},
	}
	for name, run := range commands {
		t.Run(name, func(t *testing.T) {
			divergentConfig(t)
			warnings := captureLoadWarnings(t)
			var out bytes.Buffer
			if err := run(&out); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			got := warnings.String()
			if !strings.Contains(got, `remote "llm"`) || !strings.Contains(got, "https://example.com/old.yaml") {
				t.Fatalf("stderr misses the load warning: %q", got)
			}
			if !strings.HasPrefix(got, "warning: ") {
				t.Fatalf("warning line lacks its prefix: %q", got)
			}
			if strings.Contains(out.String(), "warning:") {
				t.Fatalf("warnings leaked onto stdout:\n%s", out.String())
			}
		})
	}
}

// add and edit reach the same sink through the shared load path; the sink is
// what a non-TTY test can reach without driving the TUI.
func TestLoadWarningsReachEveryLoadPath(t *testing.T) {
	t.Run("app.Load", func(t *testing.T) {
		divergentConfig(t)
		warnings := captureLoadWarnings(t)
		cfg, err := app.Load(appOpts())
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(cfg.Warnings) == 0 {
			t.Fatal("fixture should have produced a load warning")
		}
		if !strings.Contains(warnings.String(), cfg.Warnings[0]) {
			t.Fatalf("sink = %q, want the load warning", warnings.String())
		}
	})

	t.Run("app.ResolveDefault before the exec path", func(t *testing.T) {
		divergentConfig(t)
		warnings := captureLoadWarnings(t)
		if _, _, err := app.ResolveProfiles(appOpts(), []string{"dev"}); err != nil {
			t.Fatalf("ResolveDefault: %v", err)
		}
		if !strings.Contains(warnings.String(), `remote "llm"`) {
			t.Fatalf("sink = %q, want the warning emitted before x runs the child", warnings.String())
		}
	})
}

func TestLoadWarningsSilentInCompletions(t *testing.T) {
	f := divergentConfig(t)
	warnings := captureLoadWarnings(t)

	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")
	cmd.Flags().Bool("no-local", false, "")
	_ = cmd.Flags().Set("config", f.globalPath)

	got, directive := completeProfile(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive = %v, want NoFileComp", directive)
	}
	if len(got) == 0 {
		t.Fatal("completion should still offer the merged profiles")
	}
	if warnings.Len() != 0 {
		t.Fatalf("completion wrote load warnings into the shell: %q", warnings.String())
	}
}

// A remotes directory that is not isolated by the fixtures still must not leak
// into tests through the default sink.
func TestLoadWarningsDefaultSinkIsStderr(t *testing.T) {
	if loadWarningsOut != os.Stderr {
		t.Fatalf("default warning sink = %T, want os.Stderr", loadWarningsOut)
	}
}

func TestPullCommandWarnsAboutItsOwnConfig(t *testing.T) {
	f := divergentConfig(t)
	warnings := captureLoadWarnings(t)
	withPullFlags(t, func(string, string) ([]byte, string, error) {
		return []byte(pullContent), "", nil
	})

	if _, err := runPull(t, "llm"); err != nil {
		t.Fatalf("pull llm: %v", err)
	}
	if !strings.Contains(warnings.String(), `remote "llm"`) {
		t.Fatalf("pull did not report the divergent view it loaded: %q", warnings.String())
	}
	if st, ok := config.ReadRemoteState("llm"); !ok || st.URL != "https://example.com/new.yaml" {
		t.Fatalf("sidecar = %+v ok=%v, want the re-fetch recorded", st, ok)
	}
	_ = f
}

// The default sink must be os.Stderr and stay untouched when the caller supplies
// nothing, so a warning cannot be lost by a missing wiring step.
func TestRenderLoadWarningsPrefixesEachLine(t *testing.T) {
	var buf bytes.Buffer
	prev := loadWarningsOut
	loadWarningsOut = &buf
	t.Cleanup(func() { loadWarningsOut = prev })

	renderLoadWarnings([]string{"first", "second"})
	want := "warning: first\nwarning: second\n"
	if got := buf.String(); got != want {
		t.Fatalf("renderLoadWarnings = %q, want %q", got, want)
	}
	buf.Reset()
	renderLoadWarnings(nil)
	if buf.Len() != 0 {
		t.Fatalf("no warnings must print nothing, got %q", buf.String())
	}
}
