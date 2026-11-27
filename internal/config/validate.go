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
	KindMarkedEmpty        IssueKind = "marked-empty"
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
	case KindMarkedEmpty:
		return "marked for publication but declares neither profiles nor remotes"
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
// global profile extending a remote-provided profile resolves through those
// layers and must not read as dangling. Only the profiles the global file itself
// declares are attributed to this pass; remote content's own findings belong to
// the merged pass. A load or parse failure is returned as an error, not reported
// as a valid file.
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

// danglingIssue grades one extends parent no profile defines as an error and
// carries the pull that would supply it when the parent's prefix is a declared
// but unpulled record. Strict scoping leaves no authorship to grade: a remote
// file cannot name a parent outside its own namespace, so a reference no
// profile defines is a concrete breakage whoever declared it.
func (c Config) danglingIssue(profile, parent string) Issue {
	return Issue{Profile: profile, Kind: KindDanglingExtends, Severity: SeverityError, Target: parent, Hint: c.pullHint(parent)}
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
	return fmt.Sprintf("remote %q is declared but its content is not pulled, so run %q to fetch it", name, PullCommand(name))
}
