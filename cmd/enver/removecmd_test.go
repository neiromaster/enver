package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

func TestGuardRemovable(t *testing.T) {
	cfg := config.Config{
		Default: "anth",
		Profiles: map[string]config.Profile{
			"anth":  {},
			"local": {Extends: config.Extends{"anth"}},
		},
	}
	// "anth" is extended by local -> refused.
	if err := guardRemovable(cfg, "anth"); err == nil || !strings.Contains(err.Error(), "extended by") {
		t.Fatalf("extended profile should be refused with dependents: %v", err)
	}
	// "local" is not extended (default status no longer blocks) -> removable.
	if err := guardRemovable(cfg, "local"); err != nil {
		t.Fatalf("removable profile refused: %v", err)
	}
	// A profile that is the default but has no dependents is removable too.
	solo := config.Config{Default: "solo", Profiles: map[string]config.Profile{"solo": {}}}
	if err := guardRemovable(solo, "solo"); err != nil {
		t.Fatalf("default profile without dependents refused: %v", err)
	}
}

// TestNotFoundInTarget covers the cross-layer --global hint vs plain not-found.
func TestNotFoundInTarget(t *testing.T) {
	saved := globalFlags
	t.Cleanup(func() { globalFlags = saved })

	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	global := filepath.Join(dir, "global.yaml")
	local := config.LocalPath()
	if err := config.UpsertProfile(global, "dev", config.Profile{Env: map[string]string{"A": "1"}}, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(local, "stage", config.Profile{Env: map[string]string{"A": "1"}}, false, false, nil); err != nil {
		t.Fatal(err)
	}

	globalFlags.configPath = global

	// dev is global-only: targeting local should hint --global.
	globalFlags.global = false
	if err := notFoundInTarget("dev", local); err == nil || !strings.Contains(err.Error(), "--global") {
		t.Fatalf("dev in local target: want --global hint, got %v", err)
	}
	// stage is local-only: targeting global should hint running without --global.
	globalFlags.global = true
	if err := notFoundInTarget("stage", global); err == nil || !strings.Contains(err.Error(), "without --global") {
		t.Fatalf("stage in global target: want without---global hint, got %v", err)
	}
	// ghost lives nowhere: plain not-found.
	if err := notFoundInTarget("ghost", global); err == nil || strings.Contains(err.Error(), "--global") {
		t.Fatalf("ghost: want plain not-found, got %v", err)
	}
}
