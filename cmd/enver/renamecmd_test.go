package main

import (
	"strings"
	"testing"

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
