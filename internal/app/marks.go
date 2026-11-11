package app

import (
	"maps"

	"github.com/neiromaster/enver/internal/config"
)

// WriteQuery describes one whole-env write whose secret marks must be answered
// for a caller that cannot resolve them itself: the writer sees only the file it
// rewrites, so an ancestor in the other layer is invisible to it.
type WriteQuery struct {
	// Other is the layer the write leaves untouched. Nil means the caller has no
	// such view, and the writer keeps its own in-file fallback.
	Other *config.Config
	// Target is the file being rewritten, as it stands before the write.
	Target config.Config
	// TargetIsGlobal reports that Target is the global layer, which readers fold
	// first; a local target is folded last, over the global one.
	TargetIsGlobal bool
	Name           string
	// Extends is the extends the write leaves in the target file.
	Extends config.Extends
	// ClearsFences reports that the write drops the target file's own unset list.
	ClearsFences bool
	// Keys is exactly the set of keys the write will write: the writer indexes
	// the answer by these strings.
	Keys []string
}

// WriteMarks answers the secret marks for a whole-env write. It projects the
// target file as the write leaves it — the profile takes q.Extends, and its own
// unset list goes when q.ClearsFences — then folds the two layers in the order a
// reader folds them, the one LoadMerged computes: global first, local overriding
// it. That order matters twice over. Extends concatenate base-first, so among
// parents a later one wins a shared key's value and mark; and the override
// layer's unsets become the profile's declared fences while the base layer's ride
// along as carried ones. A key the target file defines plainly still outranks an
// inherited mark. Nil Other answers nil, so the writer keeps its own in-file
// fallback.
func WriteMarks(q WriteQuery) map[string]bool {
	if q.Other == nil {
		return nil
	}
	projected := q.Target
	projected.Profiles = maps.Clone(q.Target.Profiles)
	if projected.Profiles == nil {
		projected.Profiles = map[string]config.Profile{}
	}
	p := projected.Profiles[q.Name]
	p.Extends = q.Extends
	if q.ClearsFences {
		p.Unset = nil
	}
	projected.Profiles[q.Name] = p
	var view config.Config
	if q.TargetIsGlobal {
		view = config.Merge(projected, *q.Other)
	} else {
		view = config.Merge(*q.Other, projected)
	}
	return config.MarksForWrite(view, q.Name, q.Keys)
}
