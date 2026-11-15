package config

import (
	"maps"
	"regexp"
)

// RemoteRecord is one entry of a config file's remotes: section: the source a
// remote's pristine content is pulled from and the ref pinned for it. The
// record name is the map key, since it prefixes the pulled profiles, names the
// content file, and identifies the record for pull.
type RemoteRecord struct {
	URL string `yaml:"url"`
	Ref string `yaml:"ref,omitempty"`
}

// recordNameRe matches a single-level record name: the name becomes a profile
// prefix and a file name under the remotes directory, so a slash would escape
// the prefix namespace the pull writes into.
var recordNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// ValidRecordName reports whether name is a legal remote record name.
func ValidRecordName(name string) bool {
	return recordNameRe.MatchString(name)
}

// mergeRemotes folds the override's records over the base's by name. The name
// is the key, not the URL: an overriding record replaces the inherited one
// whole — URL and ref both — while a record no higher layer redeclares
// survives. The copy keeps Merge pure, so the result shares no state with
// either input.
func mergeRemotes(base, override map[string]RemoteRecord) map[string]RemoteRecord {
	out := maps.Clone(base)
	for name, rec := range override {
		if out == nil {
			out = map[string]RemoteRecord{}
		}
		out[name] = rec
	}
	return out
}
