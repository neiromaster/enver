// internal/config/validate.go
package config

import "fmt"

// Issue is one config-health finding.
type Issue struct {
	Profile  string
	Kind     string // "dangling-extends" | "cycle" | "empty"
	Severity string // "error" | "warning"
	Target   string // dangling target
	Detail   string // cycle detail
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
		if p.Extends != "" {
			if !exists[p.Extends] {
				issues = append(issues, Issue{Profile: n, Kind: "dangling-extends", Severity: "error", Target: p.Extends})
				continue
			}
			if _, _, err := cfg.ResolveProfile(n); err != nil {
				issues = append(issues, Issue{Profile: n, Kind: "cycle", Severity: "error", Detail: err.Error()})
			}
		}
		if len(p.Env) == 0 && p.Extends == "" {
			issues = append(issues, Issue{Profile: n, Kind: "empty", Severity: "warning"})
		}
	}
	return issues
}
