package app

import (
	"maps"

	"github.com/neiromaster/enver/internal/config"
)

// WriteQuery describes one whole-env write whose secret marks must be answered
// for a caller that cannot resolve them itself: the writer sees only the file it
// rewrites, so an ancestor in any layer below it is invisible to it.
type WriteQuery struct {
	// Under is the stack beneath the target, nearest first — the layers a reader
	// folds before it reaches the target. Nil or empty means the caller has no
	// such view, and the writer keeps its own in-file fallback.
	Under []config.Layer
	// Target is the file being rewritten, as it stands before the write.
	Target config.Config
	// TargetIsGlobal reports that Target is the global layer, which readers fold
	// under a local one; it labels the target in the fold.
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
// unset list goes when q.ClearsFences — then folds the stack the target sits on
// and the target over it, in the order a reader folds them: the deepest layer
// first, each layer above it next, the target last. Every layer folds under the
// provenance label it carries, so a remote's content keeps the era its fences
// were declared in.
//
// That order matters twice over. Extends concatenate base-first, so among parents
// a later one wins a shared key's value and mark — a nearer mention decides, and
// a deliberately unmarked closer redefinition clears an inherited mark; and each
// layer's unsets take their own era: the target's declared fences while the ones
// it inherits ride along as carried. A key the target file defines plainly still
// outranks an inherited mark. Nil Under answers nil, so the writer keeps its own
// in-file fallback.
func WriteMarks(q WriteQuery) map[string]bool {
	if len(q.Under) == 0 {
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
	deepest := len(q.Under) - 1
	view := q.Under[deepest].Config
	for i := deepest - 1; i >= 0; i-- {
		view = config.Merge(view, q.Under[i].Config, q.Under[i+1].Label, q.Under[i].Label)
	}
	targetLabel := config.LayerLocal
	if q.TargetIsGlobal {
		targetLabel = config.LayerGlobal
	}
	view = config.Merge(view, projected, q.Under[0].Label, targetLabel)
	return config.MarksForWrite(view, q.Name, q.Keys)
}
