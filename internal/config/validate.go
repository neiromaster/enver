package config

import (
	"fmt"
	"sort"
	"strings"
)

// Issue is one config-health finding.
type Issue struct {
	Profile  string
	Kind     string // "dangling-extends" | "cycle" | "empty" | "contradictory-unset" | "unset-shadowed"
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
	case "unset-shadowed":
		return fmt.Sprintf("%s: %q is unset by %s but defined here; the unset wins", i.Profile, i.Target, i.Detail)
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
		issues = append(issues, shadowedUnsets(cfg, n)...)
	}
	return issues
}

// shadowedUnsets warns about own env keys the resolved profile drops because
// an inherited unset fences them: the definition is dead — the unset wins —
// and the author should hear about it. Keys unset by the profile itself are
// skipped; contradictory-unset already covers them. A key an ancestor defines
// and this profile unsets is the feature working as intended and is not
// reported. The reverse cross-layer direction is dead and reported: a local
// definition the global layer's copy of the profile unsets.
func shadowedUnsets(cfg Config, name string) []Issue {
	p := cfg.Profiles[name]
	if len(p.Env) == 0 {
		return nil
	}
	r, err := cfg.ResolveProfile(name)
	if err != nil {
		return nil // dangling-extends or cycle is reported elsewhere
	}
	if len(r.Unsets) == 0 {
		return nil
	}
	var issues []Issue
	for _, k := range sortedEnvKeys(p.Env) {
		if UnsetsHasKey(p.Unset, k) {
			// Same-layer pairs are contradictory-unset's domain, and a local
			// unset stripping a global key is the feature working as intended.
			// The one dead combination left is a local definition fenced by
			// the global layer's unset: nothing else reports it, so it is
			// reported here.
			if cfg.unsetLayer(name, k) == LayerGlobal && cfg.layerOf(name, k) == LayerLocal {
				issues = append(issues, Issue{
					Profile:  name,
					Kind:     "unset-shadowed",
					Severity: "warning",
					Target:   k,
					Detail:   "the global layer",
				})
			}
			continue
		}
		if hasEnvKey(r.Env, k) {
			continue
		}
		issues = append(issues, Issue{
			Profile:  name,
			Kind:     "unset-shadowed",
			Severity: "warning",
			Target:   k,
			Detail:   unsetDeclarer(r.Chain, cfg, k),
		})
	}
	return issues
}

// unsetDeclarer names the chain profile whose own unset fences key, or
// "an ancestor" when none matches (an unset inherited transitively).
func unsetDeclarer(chain []string, cfg Config, key string) string {
	for _, m := range chain {
		if m == chain[0] {
			continue
		}
		if UnsetsHasKey(cfg.Profiles[m].Unset, key) {
			return m
		}
	}
	return "an ancestor"
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
