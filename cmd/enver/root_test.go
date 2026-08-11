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
