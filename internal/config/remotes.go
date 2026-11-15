package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/neiromaster/enver/internal/remote/source"
	"github.com/neiromaster/enver/internal/xdg"
	"gopkg.in/yaml.v3"
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

// RemoteState is the fetch provenance recorded for a remote's stored content:
// the URL and ref it was fetched from, the commit sha a fetch resolved, and when
// the pull ran. It lives in a machine-local sidecar next to the content, so a
// checked-in config never carries machine-local state.
type RemoteState struct {
	URL      string    `yaml:"url"`
	Ref      string    `yaml:"ref,omitempty"`
	Commit   string    `yaml:"commit,omitempty"`
	PulledAt time.Time `yaml:"pulled-at"`
}

// RemotesDir is the directory holding a remote's pristine content and its state
// sidecar, one <name>.yaml and one <name>.meta per record. A var so tests and
// callers embedding enver can redirect it.
var RemotesDir = func() string {
	return filepath.Join(xdg.ConfigHome(), "enver", "remotes")
}

// RemoteContentPath is the file holding name's pristine pulled content.
func RemoteContentPath(name string) string {
	return filepath.Join(RemotesDir(), name+".yaml")
}

// remoteStatePath is the sidecar recording what name's content was fetched from.
func remoteStatePath(name string) string {
	return filepath.Join(RemotesDir(), name+".meta")
}

// ReadRemoteState reads the fetch provenance recorded for name. ok is false when
// no state can be trusted: the remote was never pulled, the sidecar is
// unreadable, or name falls outside the record-name rule. All three leave the
// content's provenance unknown, which the load-time check reports as content
// due for a re-pull rather than as content whose source is verified.
func ReadRemoteState(name string) (RemoteState, bool) {
	if !ValidRecordName(name) {
		return RemoteState{}, false
	}
	data, err := os.ReadFile(remoteStatePath(name))
	if err != nil {
		return RemoteState{}, false
	}
	var st RemoteState
	if err := yaml.Unmarshal(data, &st); err != nil {
		return RemoteState{}, false
	}
	return st, true
}

// WriteRemoteState records name's fetch provenance in its sidecar, creating the
// remotes directory. The sidecar is replaced whole through a temp file and a
// rename, so a pull that dies mid-write leaves the old state, never a truncated
// one.
func WriteRemoteState(name string, st RemoteState) error {
	if !ValidRecordName(name) {
		return fmt.Errorf("remote state: %q is not a record name", name)
	}
	data, err := yaml.Marshal(st)
	if err != nil {
		return fmt.Errorf("remote %q: encode state: %w", name, err)
	}
	if err := writeFileAtomic(remoteStatePath(name), data); err != nil {
		return fmt.Errorf("remote %q: write state: %w", name, err)
	}
	return nil
}

// writeFileAtomic replaces path with data through a temp file in the same
// directory, so a reader sees either the old bytes or the new ones.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// remoteWarnings reports the merged records whose stored content disagrees with
// the source the record points at: content fetched from another URL or ref, or
// content with no recorded provenance at all. Resolution stays offline and keeps
// serving what is on disk — the warning is how a mismatch stops being silent.
// A record with no content yet has nothing to disagree with and contributes
// nothing.
func remoteWarnings(records map[string]RemoteRecord) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(records)) {
		if _, err := os.Stat(RemoteContentPath(name)); err != nil {
			continue
		}
		url, ref, err := source.ResolveRecord(records[name].URL, records[name].Ref)
		if err != nil {
			out = append(out, fmt.Sprintf("remote %q: the record's source is unusable (%v); the stored content cannot be verified, so run \"enver pull %s\" once the record is fixed", name, err, name))
			continue
		}
		st, ok := ReadRemoteState(name)
		if !ok {
			out = append(out, fmt.Sprintf("remote %q: content has no pull state to compare, and the record points at %s; run \"enver pull %s\" to re-fetch it", name, sourceLabel(url, ref), name))
			continue
		}
		if url == st.URL && ref == st.Ref {
			continue
		}
		out = append(out, fmt.Sprintf("remote %q: content was fetched from %s, but the record now points at %s; run \"enver pull %s\" to re-sync", name, sourceLabel(st.URL, st.Ref), sourceLabel(url, ref), name))
	}
	return out
}

// sourceLabel names a resolved source in a warning: the URL alone when the
// source carries no separate ref, and the URL with its ref otherwise. A github
// ref is a segment of its URL already, so the label repeats it rather than guess
// which segment is the ref.
func sourceLabel(url, ref string) string {
	if ref == "" {
		return url
	}
	return fmt.Sprintf("%s at ref %s", url, ref)
}
