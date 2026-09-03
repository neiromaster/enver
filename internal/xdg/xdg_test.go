package xdg

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigHomeLadder(t *testing.T) {
	defer func() { homeDir = os.UserHomeDir }()

	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := ConfigHome(); got != "/xdg" {
		t.Fatalf("ConfigHome with XDG set = %q, want /xdg", got)
	}

	t.Setenv("XDG_CONFIG_HOME", "")
	homeDir = func() (string, error) { return "/home/u", nil }
	if got := ConfigHome(); got != filepath.Join("/home/u", ".config") {
		t.Fatalf("ConfigHome with home = %q, want %q", got, filepath.Join("/home/u", ".config"))
	}

	homeDir = func() (string, error) { return `C:\Users\u`, nil }
	if got := ConfigHome(); got != filepath.Join(`C:\Users\u`, ".config") {
		t.Fatalf("ConfigHome with Windows home = %q, want %q", got, filepath.Join(`C:\Users\u`, ".config"))
	}

	homeDir = func() (string, error) { return "", errors.New("home is not set") }
	if got := ConfigHome(); got != filepath.Join("/", ".config") {
		t.Fatalf("ConfigHome without a home = %q, want %q", got, filepath.Join("/", ".config"))
	}
}
