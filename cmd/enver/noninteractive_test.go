package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

// Force ui.Interactive=false explicitly rather than relying on go test's stdin.
func setNonInteractive(t *testing.T) {
	t.Helper()
	prev := ui.Interactive
	ui.Interactive = func() bool { return false }
	t.Cleanup(func() { ui.Interactive = prev })
}

func writeEnvFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "in.env")
	if err := os.WriteFile(p, []byte("X=1\n"), 0o600); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return p
}

func TestAddNonInteractiveErrors(t *testing.T) {
	setNonInteractive(t)
	withGlobalConfig(t, writeTempConfig(t, "p", map[string]string{"A": "1"}, nil, false))

	if err := doAdd(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "interactive") {
		t.Fatalf("expected interactive error, got: %v", err)
	}
	// A name argument does not make add non-interactive; var entry is a TUI flow.
	if err := doAdd(&cobra.Command{}, []string{"q"}); err == nil {
		t.Fatal("add with a name arg must still error when non-interactive")
	}
}

func TestEditNonInteractiveErrors(t *testing.T) {
	setNonInteractive(t)
	withGlobalConfig(t, writeTempConfig(t, "p", map[string]string{"A": "1"}, nil, false))

	if err := doEdit(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "interactive") {
		t.Fatalf("expected interactive error, got: %v", err)
	}
	if err := doEdit(&cobra.Command{}, []string{"p"}); err == nil {
		t.Fatal("edit with a profile arg must still error when non-interactive")
	}
}

func TestRemoveNonInteractiveMissingProfile(t *testing.T) {
	setNonInteractive(t)
	withGlobalConfig(t, writeTempConfig(t, "p", map[string]string{"A": "1"}, nil, false))
	prev := removeYes
	removeYes = false
	t.Cleanup(func() { removeYes = prev })

	if err := removeCmd.RunE(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "profile name required") {
		t.Fatalf("expected profile-name-required error, got: %v", err)
	}
}

func TestRemoveNonInteractiveNeedsYes(t *testing.T) {
	setNonInteractive(t)
	withGlobalConfig(t, writeTempConfig(t, "p", map[string]string{"A": "1"}, nil, false))
	prev := removeYes
	removeYes = false
	t.Cleanup(func() { removeYes = prev })

	if err := removeCmd.RunE(&cobra.Command{}, []string{"p"}); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected --yes hint, got: %v", err)
	}
}

func TestRemoveNonInteractiveWithYesWorks(t *testing.T) {
	setNonInteractive(t)
	path := writeTempConfig(t, "p", map[string]string{"A": "1"}, nil, false)
	withGlobalConfig(t, path)
	prev := removeYes
	removeYes = true
	t.Cleanup(func() { removeYes = prev })

	if err := removeCmd.RunE(&cobra.Command{}, []string{"p"}); err != nil {
		t.Fatalf("non-interactive remove with --yes should work: %v", err)
	}
	if _, _, _, ok, _ := config.ReadProfile(globalFlags.configPath, "p"); ok {
		t.Fatal("profile should have been removed")
	}
}

func TestImportNonInteractiveMissingProfile(t *testing.T) {
	setNonInteractive(t)
	withGlobalConfig(t, writeTempConfig(t, "p", map[string]string{"A": "1"}, nil, false))

	if err := importCmd.RunE(&cobra.Command{}, []string{writeEnvFile(t)}); err == nil || !strings.Contains(err.Error(), "profile name required") {
		t.Fatalf("expected profile-name-required error, got: %v", err)
	}
}

func TestImportNonInteractiveWithProfileWorks(t *testing.T) {
	setNonInteractive(t)
	withGlobalConfig(t, writeTempConfig(t, "p", map[string]string{"A": "1"}, nil, false))

	if err := importCmd.RunE(&cobra.Command{}, []string{writeEnvFile(t), "newprof"}); err != nil {
		t.Fatalf("non-interactive import with profile should work: %v", err)
	}
	prof, _, _, ok, _ := config.ReadProfile(globalFlags.configPath, "newprof")
	if !ok || prof.Env["X"] != "1" {
		t.Fatalf("profile not imported: %+v ok=%v", prof, ok)
	}
}

func TestRenameNonInteractiveMissingArgs(t *testing.T) {
	setNonInteractive(t)
	withGlobalConfig(t, writeTempConfig(t, "p", map[string]string{"A": "1"}, nil, false))

	if err := renameCmd.RunE(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "profile name required") {
		t.Fatalf("expected profile-name-required, got: %v", err)
	}
	if err := renameCmd.RunE(&cobra.Command{}, []string{"p"}); err == nil || !strings.Contains(err.Error(), "new name required") {
		t.Fatalf("expected new-name-required, got: %v", err)
	}
}

func TestRenameNonInteractiveWorks(t *testing.T) {
	setNonInteractive(t)
	withGlobalConfig(t, writeTempConfig(t, "p", map[string]string{"A": "1"}, nil, false))

	if err := renameCmd.RunE(&cobra.Command{}, []string{"p", "q"}); err != nil {
		t.Fatalf("non-interactive rename should work: %v", err)
	}
	if _, _, _, ok, _ := config.ReadProfile(globalFlags.configPath, "q"); !ok {
		t.Fatal("profile should have been renamed to q")
	}
}

func TestDuplicateNonInteractiveMissingArgs(t *testing.T) {
	setNonInteractive(t)
	withGlobalConfig(t, writeTempConfig(t, "p", map[string]string{"A": "1"}, nil, false))

	if err := duplicateCmd.RunE(&cobra.Command{}, nil); err == nil || !strings.Contains(err.Error(), "profile name required") {
		t.Fatalf("expected profile-name-required, got: %v", err)
	}
	if err := duplicateCmd.RunE(&cobra.Command{}, []string{"p"}); err == nil || !strings.Contains(err.Error(), "new name required") {
		t.Fatalf("expected new-name-required, got: %v", err)
	}
}

func TestDuplicateNonInteractiveWorks(t *testing.T) {
	setNonInteractive(t)
	withGlobalConfig(t, writeTempConfig(t, "p", map[string]string{"A": "1"}, nil, false))

	if err := duplicateCmd.RunE(&cobra.Command{}, []string{"p", "q"}); err != nil {
		t.Fatalf("non-interactive duplicate should work: %v", err)
	}
	if _, _, _, ok, _ := config.ReadProfile(globalFlags.configPath, "q"); !ok {
		t.Fatal("profile q should exist after duplicate")
	}
}
