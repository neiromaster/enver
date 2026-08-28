package main

import (
	"errors"
	"maps"

	"github.com/neiromaster/enver/internal/config"
)

// probeWith copies cfg and rewrites profile name through patch, so resolution
// can preview an uncommitted working copy without touching the filesystem.
func probeWith(cfg config.Config, name string, patch func(*config.Profile)) config.Config {
	probe := config.Config{Default: cfg.Default, Profiles: make(map[string]config.Profile, len(cfg.Profiles))}
	maps.Copy(probe.Profiles, cfg.Profiles)
	tp := probe.Profiles[name]
	patch(&tp)
	probe.Profiles[name] = tp
	return probe
}

// extendsCycles reports whether giving name the pending extends would leave
// its chain unresolvable through a loop. A dangling parent fails resolution
// without being a cycle — that state stays commitable and surfaces as an
// external picker row instead.
func extendsCycles(cfg config.Config, name string, extends config.Extends) bool {
	probe := probeWith(cfg, name, func(p *config.Profile) { p.Extends = extends })
	_, err := probe.ResolveProfile(name)
	return errors.Is(err, config.ErrExtendsCycle)
}

// stripWorkingFences clears the copy's declared unsets and the era-carried
// ones Merge parked beside them — a load-built config holds its fences across
// both fields, so a probe that ignores only Unset still strips through Carried.
func stripWorkingFences(p config.Profile) config.Profile {
	p.Unset = nil
	p.Carried = nil
	return p
}
