package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

// TestWriteTarget covers the write-scope rule: local .enver.yaml in cwd by
// default, the global config under --global (honoring --config).
func TestWriteTarget(t *testing.T) {
	saved := globalFlags
	t.Cleanup(func() { globalFlags = saved })

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
	saved := globalFlags
	t.Cleanup(func() { globalFlags = saved })

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

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
