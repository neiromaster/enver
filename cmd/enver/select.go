package main

import (
	"fmt"
	"strings"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
)

// profileOptions builds the interactive profile picker options (sorted), with a
// label that notes when a profile extends another. exclude is omitted.
func profileOptions(cfg config.Config, exclude string) []ui.Option {
	var opts []ui.Option
	for _, n := range cfg.ProfileNames() {
		if n == exclude {
			continue
		}
		label := n
		if e := cfg.Profiles[n].Extends; len(e) > 0 {
			label = fmt.Sprintf("%s (extends → %s)", n, strings.Join(e, ", "))
		}
		opts = append(opts, ui.Option{Value: n, Label: label})
	}
	return opts
}

// pickProfile prompts the user to choose a profile; returns "" on abort.
func pickProfile(cfg config.Config, title, exclude string) (string, error) {
	opts := profileOptions(cfg, exclude)
	if len(opts) == 0 {
		return "", nil
	}
	return ui.Select(title, opts)
}
