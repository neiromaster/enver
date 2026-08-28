package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestDefaultCorruptTargetFileErrorsLoudly pins that a broken write-target file
// surfaces its parse error instead of masquerading as a missing profile.
// Reachable only when reading skips the target: under --no-local app.Load
// never opens the local file, so the target reload is the first to trip.
func TestDefaultCorruptTargetFileErrorsLoudly(t *testing.T) {
	global := writeTempConfig(t, "prod", map[string]string{"A": "1"}, nil, true)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".enver.yaml"), []byte("profiles: [broken"), 0o644); err != nil {
		t.Fatalf("write corrupt local config: %v", err)
	}
	t.Chdir(dir)

	savedConfig, savedNoLocal, savedGlobal := globalFlags.configPath, globalFlags.noLocal, globalFlags.global
	globalFlags.configPath = global
	globalFlags.noLocal = true
	globalFlags.global = false
	t.Cleanup(func() {
		globalFlags.configPath, globalFlags.noLocal, globalFlags.global = savedConfig, savedNoLocal, savedGlobal
	})

	err := defaultCmd.RunE(&cobra.Command{}, []string{"prod"})
	if err == nil {
		t.Fatal("expected a parse error for the corrupt target file, got nil")
	}
	if strings.Contains(err.Error(), "not found") {
		t.Fatalf("corrupt target file misreported as missing profile: %v", err)
	}
}
