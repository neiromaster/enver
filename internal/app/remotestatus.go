package app

import (
	"maps"
	"os"
	"slices"
	"time"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/remote"
	"gopkg.in/yaml.v3"
)

// RemoteStatus is one record the merged view declares and the freshness of the
// content stored for it: the source the record resolves to, whether that source
// is pinned, and what the sidecar records about the last pull. It is what
// enver pull --list renders.
type RemoteStatus struct {
	Name string
	// Depth is how deep the record sits in its tree: 1 for a record the config
	// declares, one more per stored declaration below it.
	Depth int
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

// RemoteStatuses lists every record the merged view declares plus every child
// its stored content declares, recursively, depth-first with siblings sorted:
// the same tree a pull stores and the summary indents. Declarations are read
// the light way — the plain parse of stored content — since a listing needs no
// projection, and content that is absent or unparseable ends its walk there,
// where load and pull already report it. It only reads: no fetch and no write,
// so a listing never changes what a later resolve sees.
func RemoteStatuses(cfg config.Config) []RemoteStatus {
	out := make([]RemoteStatus, 0, len(cfg.Remotes))
	seen := make(map[string]bool, len(cfg.Remotes))
	var walk func(path string, rec config.RemoteRecord, depth int)
	walk = func(path string, rec config.RemoteRecord, depth int) {
		if seen[path] {
			return
		}
		seen[path] = true
		out = append(out, remoteStatus(path, rec, depth))
		data, err := os.ReadFile(config.RemoteContentPath(path))
		if err != nil {
			return
		}
		var stored config.Config
		if err := yaml.Unmarshal(data, &stored); err != nil {
			return
		}
		for _, name := range slices.Sorted(maps.Keys(stored.Remotes)) {
			walk(path+"/"+name, stored.Remotes[name], depth+1)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Remotes)) {
		walk(name, cfg.Remotes[name], 1)
	}
	return out
}

// remoteStatus assembles one record's row. An unresolvable source keeps the
// spelling the record holds and contributes no pin state: the load path already
// reports what the resolver could not read.
func remoteStatus(name string, rec config.RemoteRecord, depth int) RemoteStatus {
	status := RemoteStatus{Name: name, Depth: depth}
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
