// Package xdg resolves the XDG locations shared by the config path and the
// key-file path, so the two ladders cannot drift.
package xdg

import (
	"os"
	"path/filepath"
)

// homeDir stands in for os.UserHomeDir in tests.
var homeDir = os.UserHomeDir

// ConfigHome returns the XDG config home: $XDG_CONFIG_HOME when set, else
// $HOME/.config, falling back to the platform home directory from
// os.UserHomeDir (%USERPROFILE% on Windows) when $HOME is unset. A missing
// home falls back to "/" so the result stays an absolute path either way.
func ConfigHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	if home := os.Getenv("HOME"); home != "" {
		return filepath.Join(home, ".config")
	}
	home, err := homeDir()
	if err != nil || home == "" {
		home = "/"
	}
	return filepath.Join(home, ".config")
}
