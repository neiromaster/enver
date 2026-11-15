package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/neiromaster/enver/internal/crypto"
	"github.com/neiromaster/enver/internal/envname"
)

// IssueKind labels the class of a config-health finding.
type IssueKind string

const (
	KindDanglingExtends    IssueKind = "dangling-extends"
	KindCycle              IssueKind = "cycle"
	KindEmpty              IssueKind = "empty"
	KindContradictoryUnset IssueKind = "contradictory-unset"
	KindPlaintextSecret    IssueKind = "plaintext-secret"
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
	Hint     string // actionable lead: which remote expects a missing parent, the pull that supplies it
	File     string // source scope when known (e.g. "global"); "" means the merged view
}

func (i Issue) String() string {
	return fmt.Sprintf("%s: %s", i.Profile, i.Reason())
}

// Reason renders the finding without the profile name, so a caller that names
// the profile itself does not repeat it.
func (i Issue) Reason() string {
	switch i.Kind {
	case KindDanglingExtends:
		reason := fmt.Sprintf("extends %q which does not exist", i.Target)
		if i.Hint != "" {
			reason += " (" + i.Hint + ")"
		}
		return reason
	case KindCycle:
		return fmt.Sprintf("extends cycle (%s)", i.Detail)
	case KindEmpty:
		return "no env vars, no extends, and no unset"
	case KindContradictoryUnset:
		return fmt.Sprintf("unsets %q which it also defines in env", i.Target)
	case KindPlaintextSecret:
		return fmt.Sprintf("%q is marked secret but stored in plaintext", i.Target)
	}
	return string(i.Kind)
}

// Validate audits cfg for dangling extends, cycles, no-op profiles, unset
// contradictions, and marked secrets still stored in plaintext.
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
				issues = append(issues, cfg.danglingIssue(n, parent))
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
		for _, k := range slices.Sorted(maps.Keys(p.Env)) {
			if p.Marked[k] && !crypto.IsEncrypted(p.Env[k]) {
				issues = append(issues, Issue{Profile: n, Kind: KindPlaintextSecret, Severity: SeverityWarning, Target: k})
			}
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

// ValidateGlobal audits the global config over the remote layers it sits on
// (File="global"), catching a global profile that extends a name only the local
// file provides. Remote content is a lower layer, not a project-local one, so a
// global profile extending a remote-provided profile is exactly what the
// passthrough rule enables and must not read as dangling. Only the profiles the
// global file itself declares are attributed to this pass; remote content's own
// findings belong to the merged pass. A load or parse failure is returned as an
// error, not reported as a valid file.
func ValidateGlobal(globalPath string) ([]Issue, error) {
	global, err := load(GlobalPath(globalPath))
	if err != nil {
		return nil, err
	}
	view, err := LoadMerged(globalPath, false)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, is := range Validate(view) {
		if _, declared := global.Profiles[is.Profile]; !declared {
			continue
		}
		is.File = "global"
		issues = append(issues, is)
	}
	return issues, nil
}

// danglingIssue grades one extends parent no profile defines by the layer whose
// copy declared the reference. The passthrough rule deliberately lets a remote
// name a parent the user or another remote owns, which may simply not exist
// yet: that is the remote's gap, warning-grade, and the hint names the remote
// expecting it. The user's own declarations stay errors — including one they
// added to a profile a remote otherwise owns, which is why the grading reads
// the target's authorship rather than the profile's current owner. Either way a
// target whose prefix names an unpulled record carries the pull that supplies
// it.
func (c Config) danglingIssue(profile, parent string) Issue {
	is := Issue{Profile: profile, Kind: KindDanglingExtends, Severity: SeverityError, Target: parent}
	layer := c.extendsLayer(profile, parent)
	if !IsRemoteLayer(layer) {
		is.Hint = c.pullHint(parent)
		return is
	}
	remote, _ := RemoteName(layer)
	is.Severity = SeverityWarning
	is.Hint = fmt.Sprintf("declared by remote %q, which expects the parent from your own profiles or another remote", remote)
	if pull := c.pullHint(parent); pull != "" {
		is.Hint = fmt.Sprintf("declared by remote %q; %s", remote, pull)
	}
	return is
}

// extendsLayer reports the layer whose copy of profile declared the extends
// target. A remote's reference stays the remote's after a user patch restamps
// the profile name, because the patch's copy restamps only the targets it names
// itself. Content with no recorded authorship — a config no fold built, the
// isolated global file — falls back to the profile's declaring layer.
func (c Config) extendsLayer(profile, target string) string {
	if layer, ok := c.ExtendsOrigins[profile][target]; ok {
		return layer
	}
	if layer, ok := c.ProfileLayer[profile]; ok {
		return layer
	}
	return LayerGlobal
}

// pullHint names the pull that would supply parent when its prefix is a record
// declared but never pulled. It stays empty when no pull can help: an
// undeclared prefix is a typo, and a pulled record that lacks the profile
// upstream will not gain it by fetching again.
func (c Config) pullHint(parent string) string {
	name, rest, ok := strings.Cut(parent, "/")
	if !ok || rest == "" || !ValidRecordName(name) {
		return ""
	}
	if _, declared := c.Remotes[name]; !declared {
		return ""
	}
	if fi, err := os.Stat(RemoteContentPath(name)); err == nil && !fi.IsDir() {
		return ""
	}
	return fmt.Sprintf("remote %q is declared but its content is not pulled, so run \"enver pull %s\" to fetch it", name, name)
}

// RemoteDanglingIssues returns the dangling-extends findings reachable from name
// that a remote-authored reference causes. A non-empty result means name cannot
// resolve only because a remote expects a parent no layer defines yet, which a
// pull or a user profile can still supply. It is empty when name resolves, when
// its chain loops, and when the user's own references break it: none of those is
// a remote's gap, so a caller must keep failing on them.
func (c Config) RemoteDanglingIssues(name string) []Issue {
	visiting := map[string]bool{}
	done := map[string]bool{}
	var issues []Issue
	var walk func(string) bool
	walk = func(current string) bool {
		if done[current] {
			return true
		}
		if visiting[current] {
			return false
		}
		p, ok := c.Profiles[current]
		if !ok {
			return false
		}
		visiting[current] = true
		for _, parent := range p.Extends {
			if _, defined := c.Profiles[parent]; !defined {
				if !IsRemoteLayer(c.extendsLayer(current, parent)) {
					return false
				}
				issues = append(issues, c.danglingIssue(current, parent))
				continue
			}
			if !walk(parent) {
				return false
			}
		}
		delete(visiting, current)
		done[current] = true
		return true
	}
	if !walk(name) {
		return nil
	}
	return issues
}
