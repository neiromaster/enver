package app

import (
	"maps"
	"os"
	"slices"
	"time"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/remote"
)

// RemoteStatus is one record the merged view declares and the freshness of the
// content stored for it: the source the record resolves to, whether that source
// is pinned, and what the sidecar records about the last pull. It is what
// enver pull --list renders.
type RemoteStatus struct {
	Name string
	// Source is the source as a reader should see it: the record's spelling with
	// the ref it resolves to when the record pins one, the record's spelling
	// alone when it does not. Ref carries the resolved ref either way.
	Source string
	Ref    string
	Pinned bool
	// Stored reports that a content file is there, or cannot be shown to be
	// absent — which is not the same as never pulled; Pulled that a sidecar
	// attests a fetch. Stored without Pulled is content no pull state can vouch
	// for, Pulled without Stored a sidecar whose content was removed.
	Stored bool
	Pulled bool
	// Commit and PulledAt are what the last pull recorded; zero when the sidecar
	// carries neither. An unpinned record's commit is the only fixed point its
	// content has.
	Commit   string
	PulledAt time.Time
	// Stale marks stored content the sidecar says came from another source;
	// StaleFrom labels that source.
	Stale     bool
	StaleFrom string
}

// RemoteStatuses lists every record the merged view declares, sorted by name,
// with the provenance of its stored content. It only reads: no fetch and no
// write, so a listing never changes what a later resolve sees.
func RemoteStatuses(cfg config.Config) []RemoteStatus {
	out := make([]RemoteStatus, 0, len(cfg.Remotes))
	for _, name := range slices.Sorted(maps.Keys(cfg.Remotes)) {
		out = append(out, remoteStatus(name, cfg.Remotes[name]))
	}
	return out
}

// remoteStatus assembles one record's row. An unresolvable source keeps the
// spelling the record holds and contributes no pin state: the load path already
// reports what the resolver could not read.
func remoteStatus(name string, rec config.RemoteRecord) RemoteStatus {
	status := RemoteStatus{Name: name}
	url, ref, err := remote.ResolveRecord(rec)
	if err != nil {
		status.Source = rec.URL
		status.Ref = rec.Ref
	} else {
		status.Ref = ref
		status.Pinned = !remote.Unpinned(ref)
		if status.Pinned {
			status.Source = sourceLabel(rec, ref)
		} else {
			status.Source = rec.URL
		}
	}
	if _, err := os.Stat(config.RemoteContentPath(name)); err == nil || !os.IsNotExist(err) {
		status.Stored = true
	}
	st, ok := config.ReadRemoteState(name)
	if !ok {
		return status
	}
	status.Pulled = true
	status.Commit = st.Commit
	status.PulledAt = st.PulledAt
	if status.Stored && url != "" && (st.URL != url || st.Ref != ref) {
		status.Stale = true
		status.StaleFrom = sourceLabelURL(st.URL, st.Ref)
	}
	return status
}
