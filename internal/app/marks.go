package app

import (
	"maps"

	"github.com/neiromaster/enver/internal/config"
)

// WriteMarks answers the secret-mark question for the keys of a whole-env write
// into profile, which the writer cannot answer itself when an ancestor lives in
// the other layer. other is that layer — the file the write leaves untouched, nil
// when the caller has no such view (then the writer keeps its own in-file
// fallback) — and target is the file being rewritten exactly as it stands now.
// The profile is projected as the write leaves it: extends replaces its extends,
// and clearsFences drops the unsets a wholesale replace wipes. The two layers are
// then folded, so an extends the other layer contributes survives, and a key the
// target file defines plainly still outranks an inherited mark. The caller must
// request exactly the keys it is about to write: the writer indexes the returned
// map by those strings.
func WriteMarks(other *config.Config, target config.Config, name string, extends config.Extends, clearsFences bool, keys []string) map[string]bool {
	if other == nil {
		return nil
	}
	projected := target
	projected.Profiles = maps.Clone(target.Profiles)
	if projected.Profiles == nil {
		projected.Profiles = map[string]config.Profile{}
	}
	p := projected.Profiles[name]
	p.Extends = extends
	if clearsFences {
		p.Unset = nil
	}
	projected.Profiles[name] = p
	return config.MarksForWrite(config.Merge(*other, projected), name, keys)
}
