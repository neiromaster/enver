package xdg

import "testing"

func TestConfigHomeLadder(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	t.Setenv("HOME", "/home/u")
	if got := ConfigHome(); got != "/xdg" {
		t.Fatalf("ConfigHome with XDG set = %q, want /xdg", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := ConfigHome(); got != "/home/u/.config" {
		t.Fatalf("ConfigHome fallback = %q, want /home/u/.config", got)
	}
	t.Setenv("HOME", "")
	if got := ConfigHome(); got != "/.config" {
		t.Fatalf("ConfigHome without HOME = %q, want /.config", got)
	}
}
