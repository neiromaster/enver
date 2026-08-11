package config

import (
	"fmt"
	"strings"
)

// Issue is one config-health finding.
type Issue struct {
	Profile  string
	Kind     string // "dangling-extends" | "cycle" | "empty"
	Severity string // "error" | "warning"
	Target   string // dangling target
	Detail   string // cycle detail
	File     string // source scope when known (e.g. "global"); "" means the merged view
}

func (i Issue) String() string {
	switch i.Kind {
	case "dangling-extends":
		return fmt.Sprintf("%s: extends %q which does not exist", i.Profile, i.Target)
	case "cycle":
		return fmt.Sprintf("%s: extends cycle (%s)", i.Profile, i.Detail)
	case "empty":
		return fmt.Sprintf("%s: no env vars and no extends", i.Profile)
	}
	return fmt.Sprintf("%s: %s", i.Profile, i.Kind)
}

// Validate audits cfg for dangling extends, cycles, and no-op profiles.
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
			if _, _, err := cfg.ResolveProfile(n); err != nil {
				if strings.Contains(err.Error(), "cycle") {
					issues = append(issues, Issue{Profile: n, Kind: "cycle", Severity: "error", Detail: err.Error()})
				}
			}
		}
		if len(p.Env) == 0 && len(p.Extends) == 0 {
			issues = append(issues, Issue{Profile: n, Kind: "empty", Severity: "warning"})
		}
	}
	return issues
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
