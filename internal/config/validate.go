package config

import (
	"errors"
	"fmt"

	"github.com/neiromaster/enver/internal/envname"
)

// IssueKind labels the class of a config-health finding.
type IssueKind string

const (
	KindDanglingExtends    IssueKind = "dangling-extends"
	KindCycle              IssueKind = "cycle"
	KindEmpty              IssueKind = "empty"
	KindContradictoryUnset IssueKind = "contradictory-unset"
)

// Severity grades how loudly an Issue surfaces.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Issue is one config-health finding.
type Issue struct {
	Profile  string
	Kind     IssueKind
	Severity Severity
	Target   string // dangling target, unset key
	Detail   string // cycle detail (chain path)
	File     string // source scope when known (e.g. "global"); "" means the merged view
}

func (i Issue) String() string {
	switch i.Kind {
	case KindDanglingExtends:
		return fmt.Sprintf("%s: extends %q which does not exist", i.Profile, i.Target)
	case KindCycle:
		return fmt.Sprintf("%s: extends cycle (%s)", i.Profile, i.Detail)
	case KindEmpty:
		return fmt.Sprintf("%s: no env vars, no extends, and no unset", i.Profile)
	case KindContradictoryUnset:
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
				issues = append(issues, Issue{Profile: n, Kind: KindDanglingExtends, Severity: SeverityError, Target: parent})
			}
		}
		if len(p.Extends) > 0 {
			if _, err := cfg.ResolveProfile(n); err != nil {
				if errors.Is(err, ErrExtendsCycle) {
					issues = append(issues, Issue{Profile: n, Kind: KindCycle, Severity: SeverityError, Detail: err.Error()})
				}
			}
		}
		if len(p.Env) == 0 && len(p.Extends) == 0 && len(p.Unset) == 0 {
			issues = append(issues, Issue{Profile: n, Kind: KindEmpty, Severity: SeverityWarning})
		}
		for _, u := range p.Unset {
			if !envname.Has(p.Env, u) {
				continue
			}
			// A definition and an unset from different layers is the documented
			// cross-layer fence (a local unset stripping a shared global key),
			// not a contradiction; only same-layer pairs misconfigure one file.
			if cfg.unsetLayer(n, u) != cfg.layerOf(n, u) {
				continue
			}
			issues = append(issues, Issue{Profile: n, Kind: KindContradictoryUnset, Severity: SeverityWarning, Target: u})
		}
	}
	return issues
}

// ValidateGlobal audits the global config in isolation (File="global"), catching
// a global profile that extends a local-only name. A load or parse failure is
// returned as an error, not reported as a valid file.
func ValidateGlobal(globalPath string) ([]Issue, error) {
	cfg, err := LoadFile(globalPath)
	if err != nil {
		return nil, err
	}
	issues := Validate(cfg)
	for i := range issues {
		issues[i].File = "global"
	}
	return issues, nil
}
