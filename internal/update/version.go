// Package update implements enver update: fetching the latest release,
// detecting how enver was installed, and delegating to the owning package
// manager or replacing a directly-installed binary from GitHub releases.
package update

import (
	"regexp"

	"golang.org/x/mod/semver"
)

// pseudoRe matches a Go pseudo-version -g<hash> suffix (release tags enver is
// built from never carry one).
var pseudoRe = regexp.MustCompile(`-g[0-9a-fA-F]{7,}`)

// canonicalVersion normalizes a version string for semver comparison: a
// v-prefix is ensured and a trailing -g<hash> pseudo-version suffix is
// stripped. Anything that is still not valid semver (dev, (devel), empty)
// falls back to v0.0.0, which compares below every real release.
func canonicalVersion(v string) string {
	v = pseudoRe.ReplaceAllString(v, "")
	if v != "" && v[0] != 'v' {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "v0.0.0"
	}
	return semver.Canonical(v)
}

// LatestOrEqual reports whether current is at or above latest.
func LatestOrEqual(current, latest string) bool {
	return semver.Compare(canonicalVersion(current), canonicalVersion(latest)) >= 0
}
