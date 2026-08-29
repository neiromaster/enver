package xdg

import (
	"path/filepath"
	"testing"
)

func TestConfigHomeLadder(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	t.Setenv("HOME", "/home/u")
	if got := ConfigHome(); got != "/xdg" {
		t.Fatalf("ConfigHome with XDG set = %q, want /xdg", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := ConfigHome(); got != filepath.Join("/home/u", ".config") {
		t.Fatalf("ConfigHome fallback = %q, want %q", got, filepath.Join("/home/u", ".config"))
	}
	t.Setenv("HOME", "")
	if got := ConfigHome(); got != filepath.Join("/", ".config") {
		t.Fatalf("ConfigHome without HOME = %q, want %q", got, filepath.Join("/", ".config"))
	}
}
