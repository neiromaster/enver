// Package xdg resolves the XDG locations shared by the config path and the
// key-file path, so the two ladders cannot drift.
package xdg

import (
	"os"
	"path/filepath"
)

// ConfigHome returns the XDG config home: $XDG_CONFIG_HOME when set, else
// $HOME/.config, with an empty $HOME treated as "/" so the result stays an
// absolute path either way.
func ConfigHome() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "/"
	}
	return filepath.Join(home, ".config")
}
