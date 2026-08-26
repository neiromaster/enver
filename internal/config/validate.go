package config

import (
	"fmt"
	"sort"
	"strings"
)

// Issue is one config-health finding.
type Issue struct {
	Profile  string
	Kind     string // "dangling-extends" | "cycle" | "empty" | "contradictory-unset"
	Severity string // "error" | "warning"
	Target   string // dangling target, unset key
	Detail   string // cycle detail, unset-declaring profile
	File     string // source scope when known (e.g. "global"); "" means the merged view
}

func (i Issue) String() string {
	switch i.Kind {
	case "dangling-extends":
		return fmt.Sprintf("%s: extends %q which does not exist", i.Profile, i.Target)
	case "cycle":
		return fmt.Sprintf("%s: extends cycle (%s)", i.Profile, i.Detail)
	case "empty":
		return fmt.Sprintf("%s: no env vars, no extends, and no unset", i.Profile)
	case "contradictory-unset":
		return fmt.Sprintf("%s: unsets %q which it also defines in env", i.Profile, i.Target)
	}
	return fmt.Sprintf("%s: %s", i.Profile, i.Kind)
}

// Validate audits cfg for dangling extends, cycles, no-op profiles, and unset
// contradictions.
func Validate(cfg Config) []Issue {
	exists := map[string]bool{}
	for n := range cfg.Profiles {
		exists[n] = true
	}
	var issues []Issue
	for _, n := range cfg.ProfileNames() {
		p := cfg.Profiles[n]
		for _, parent := range p.Extends {
			if !exists[parent] {
				issues = append(issues, Issue{Profile: n, Kind: "dangling-extends", Severity: "error", Target: parent})
			}
		}
		if len(p.Extends) > 0 {
			if _, err := cfg.ResolveProfile(n); err != nil {
				if strings.Contains(err.Error(), "cycle") {
					issues = append(issues, Issue{Profile: n, Kind: "cycle", Severity: "error", Detail: err.Error()})
				}
			}
		}
		if len(p.Env) == 0 && len(p.Extends) == 0 && len(p.Unset) == 0 {
			issues = append(issues, Issue{Profile: n, Kind: "empty", Severity: "warning"})
		}
		for _, u := range p.Unset {
			if !hasEnvKey(p.Env, u) {
				continue
			}
			// A definition and an unset from different layers is the documented
			// cross-layer fence (a local unset stripping a shared global key),
			// not a contradiction; only same-layer pairs misconfigure one file.
			if cfg.unsetLayer(n, u) != cfg.layerOf(n, u) {
				continue
			}
			issues = append(issues, Issue{Profile: n, Kind: "contradictory-unset", Severity: "warning", Target: u})
		}
	}
	return issues
}

// sortedEnvKeys returns the keys of env sorted, for stable issue order.
func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ValidateGlobal audits the global config in isolation (File="global"), catching
// a global profile that extends a local-only name.
func ValidateGlobal(globalPath string) []Issue {
	cfg, err := LoadFile(globalPath)
	if err != nil {
		return nil
	}
	issues := Validate(cfg)
	for i := range issues {
		issues[i].File = "global"
	}
	return issues
}
