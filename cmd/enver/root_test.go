package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

// TestWriteTarget covers the write-scope rule: local .enver.yaml in cwd by
// default, the global config under --global (honoring --config).
func TestWriteTarget(t *testing.T) {
	saveGlobalFlags(t)

	// EvalSymlinks: on macOS t.TempDir() returns /var/... but Getwd resolves the
	// /var → /private/var symlink, so compare against the resolved path.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	globalFlags.configPath = ""
	globalFlags.global = false
	if got, want := writeTarget(), filepath.Join(dir, config.LocalFilename); got != want {
		t.Fatalf("local target = %q, want %q", got, want)
	}

	globalFlags.global = true
	if got, want := writeTarget(), config.GlobalPath(""); got != want {
		t.Fatalf("global target = %q, want %q", got, want)
	}

	globalFlags.configPath = "/custom/g.yaml"
	if got := writeTarget(); got != "/custom/g.yaml" {
		t.Fatalf("--config override ignored on global target: %q", got)
	}
}

func TestApplyChdir(t *testing.T) {
	saveGlobalFlags(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// Empty --chdir leaves cwd untouched.
	globalFlags.chdir = ""
	if err := applyChdir(); err != nil {
		t.Fatalf("applyChdir() with empty chdir: %v", err)
	}
	if got, _ := os.Getwd(); got != wd {
		t.Fatalf("cwd changed to %q despite empty --chdir", got)
	}

	// Non-empty --chdir switches, and LocalPath resolves under it.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Restore cwd before TempDir's cleanup removes dir: cleanups run LIFO, and
	// Windows cannot remove a directory that is still the process cwd.
	t.Cleanup(func() { _ = os.Chdir(wd) })
	globalFlags.chdir = dir
	if err := applyChdir(); err != nil {
		t.Fatalf("applyChdir(): %v", err)
	}
	if got, _ := os.Getwd(); got != dir {
		t.Fatalf("cwd = %q, want %q", got, dir)
	}
	if got, want := config.LocalPath(), filepath.Join(dir, config.LocalFilename); got != want {
		t.Fatalf("LocalPath after chdir = %q, want %q", got, want)
	}

	// A bogus directory surfaces as an error.
	globalFlags.chdir = filepath.Join(dir, "does-not-exist")
	if err := applyChdir(); err == nil {
		t.Fatal("applyChdir() into a missing directory should fail")
	}
}

// TestCompletionAppliesChdir pins that --chdir reaches profile completion.
// PersistentPreRunE (which applies chdir for normal runs) does not run during
// shell completion, so the completion functions must apply it themselves.
func TestCompletionAppliesChdir(t *testing.T) {
	saveGlobalFlags(t)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	proj, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(filepath.Join(proj, config.LocalFilename), "projdev", config.Profile{Env: map[string]string{"A": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}

	// Neutral cwd with no local config; --chdir points at the project.
	neutral, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Restore cwd before TempDir's cleanup removes the dirs: cleanups run LIFO,
	// and Windows cannot remove a directory that is still the process cwd.
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(neutral); err != nil {
		t.Fatal(err)
	}
	globalFlags.chdir = proj

	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")
	cmd.Flags().Bool("global", false, "")
	cmd.Flags().Bool("no-local", false, "")

	got, directive := completeProfileInTarget(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive = %v, want NoFileComp", directive)
	}
	if len(got) != 1 || got[0] != "projdev" {
		t.Fatalf("completion = %v, want [projdev] (from --chdir target)", got)
	}
}

// TestNotFoundInTargetHintsRemotePatch pins the miss a remote-only name produces:
// remote content is a read-only base, so the writer's remedy is a patch copy in
// the user layer, and the hint names both the remote and the command that makes
// one. A name no layer provides keeps the plain miss.
func TestNotFoundInTargetHintsRemotePatch(t *testing.T) {
	_, merged := remoteWriteFixture(t, "profiles:\n  ollama:\n    env:\n      MODEL: llama\n")
	config.HintFlags = ""
	const name = "llm/ollama"
	if _, ok := merged.Profiles[name]; !ok {
		t.Fatalf("fixture premise: %q must resolve through the remote projection (%v)", name, merged.ProfileNames())
	}

	err := notFoundInTarget(name, config.LocalPath(), "default")
	if err == nil {
		t.Fatal("a remote-only name must not resolve in the write target")
	}
	for _, want := range []string{`remote "llm"`, "add a patch copy first", "enver add " + name} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to carry %q", err, want)
		}
	}

	if err := notFoundInTarget("ghost", config.LocalPath(), "default"); err == nil || err.Error() != `profile "ghost" not found` {
		t.Fatalf("a name no layer provides must keep the plain miss, got %v", err)
	}

	// The patch copy lands in the write target, so the hint carries the scope
	// flag the refused write carried.
	globalFlags.global = true
	err = notFoundInTarget(name, config.GlobalPath(globalFlags.configPath), "default")
	if err == nil || !strings.Contains(err.Error(), "enver add -g "+name) {
		t.Fatalf("a global-scope miss must hint the scoped patch copy, got %v", err)
	}
}
