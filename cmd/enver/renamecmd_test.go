package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

// TestRenameSameNameMissingProfileErrors pins that renaming a non-existent
// profile onto itself reports the absence instead of exiting 0 with a
// success-like "already named that" message.
func TestRenameSameNameMissingProfileErrors(t *testing.T) {
	path := writeTempConfig(t, "prod", map[string]string{"A": "1"}, nil, true)
	withGlobalConfig(t, path)

	err := renameCmd.RunE(&cobra.Command{}, []string{"stageing", "stageing"})
	if err == nil {
		t.Fatal("expected error renaming a non-existent profile onto itself, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a 'not found' error, got: %v", err)
	}
}

// TestRenameDifferentNameMissingProfileErrors guards the other path: a typo'd
// old name paired with a distinct new name must still surface the absence.
func TestRenameDifferentNameMissingProfileErrors(t *testing.T) {
	path := writeTempConfig(t, "prod", map[string]string{"A": "1"}, nil, true)
	withGlobalConfig(t, path)

	err := renameCmd.RunE(&cobra.Command{}, []string{"stageing", "staging"})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a 'not found' error, got: %v", err)
	}
}

// TestRenameToExistingNameErrors pins that the refusal comes from
// config.RenameProfile itself — the command adds no duplicate pre-check.
func TestRenameToExistingNameErrors(t *testing.T) {
	path := writeTempConfig(t, "prod", map[string]string{"A": "1"}, nil, true)
	if err := config.UpsertProfile(path, "stage", config.Profile{Env: map[string]string{"B": "2"}}, false, false); err != nil {
		t.Fatalf("upsert stage: %v", err)
	}
	withGlobalConfig(t, path)

	err := renameCmd.RunE(&cobra.Command{}, []string{"prod", "stage"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected an already-exists error, got: %v", err)
	}
}

// TestRenameRefusesWhenExtendedFromOtherLayer pins the merged-view guard:
// renaming a global profile that a local profile extends must refuse the
// same way remove does, because RenameProfile rewrites extends refs in the
// target file only and would dangle the cross-layer child.
func TestRenameRefusesWhenExtendedFromOtherLayer(t *testing.T) {
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
	if err := config.UpsertProfile(global, "base", config.Profile{Env: map[string]string{"A": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(local, "child", config.Profile{Extends: config.Extends{"base"}}, false, false); err != nil {
		t.Fatal(err)
	}

	globalFlags.configPath = global
	globalFlags.global = true
	globalFlags.noLocal = false

	err = renameCmd.RunE(&cobra.Command{}, []string{"base", "base-v2"})
	if err == nil || !strings.Contains(err.Error(), `refusing to rename "base"`) {
		t.Fatalf("err=%v, want extended-by refusal for rename", err)
	}
	data, rerr := os.ReadFile(global)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(data), "base") || strings.Contains(string(data), "base-v2") {
		t.Fatalf("global file was modified despite refusal: %s", data)
	}
}
