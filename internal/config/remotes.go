package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

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
	// NoPrefix is the pull mode the content was stored under: its profiles keep
	// their bare upstream names instead of landing under the record prefix. The
	// mode lives in the record because it is how the namespace already reads — a
	// re-pull that guessed from flags would silently rewrite what profiles
	// resolve to.
	NoPrefix bool `yaml:"no_prefix,omitempty"`
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

// RemoteStatePath is the sidecar recording what name's content was fetched from.
// Callers that need to inspect or remove the file use this rather than build the
// name themselves, so the layout stays in one place.
func RemoteStatePath(name string) string {
	return filepath.Join(RemotesDir(), name+".meta")
}

// ReadRemoteState reads the fetch provenance recorded for name. ok is false when
// no state can be trusted: the remote was never pulled, the sidecar is
// unreadable or records no source URL, or name falls outside the record-name
// rule. All of those leave the content's provenance unknown, which the load-time
// check reports as content due for a re-pull rather than as content whose source
// is verified.
func ReadRemoteState(name string) (RemoteState, bool) {
	if !ValidRecordName(name) {
		return RemoteState{}, false
	}
	data, err := os.ReadFile(RemoteStatePath(name))
	if err != nil {
		return RemoteState{}, false
	}
	var st RemoteState
	if err := yaml.Unmarshal(data, &st); err != nil {
		return RemoteState{}, false
	}
	if st.URL == "" {
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
		return fmt.Errorf("remote %q: not a valid record name", name)
	}
	data, err := yaml.Marshal(st)
	if err != nil {
		return fmt.Errorf("remote %q: encode state: %w", name, err)
	}
	if err := writeFileAtomic(RemoteStatePath(name), data, 0o600); err != nil {
		return fmt.Errorf("remote %q: write state: %w", name, err)
	}
	return nil
}

// WriteRemoteContent replaces name's pristine content with data, creating the
// remotes directory. The file is replaced whole through a temp file and a
// rename, so a pull that dies mid-write leaves the previous content, never a
// truncated one.
func WriteRemoteContent(name string, data []byte) error {
	if !ValidRecordName(name) {
		return fmt.Errorf("remote %q: not a valid record name", name)
	}
	if err := writeFileAtomic(RemoteContentPath(name), data, 0o600); err != nil {
		return fmt.Errorf("remote %q: write content: %w", name, err)
	}
	return nil
}

// RemoveRemoteContent deletes name's stored content. Content already absent is
// not an error: the call reports that no content is left, which is what a pull
// dropping a record's last declaration wants.
func RemoveRemoteContent(name string) error {
	return removeRemoteFile(name, RemoteContentPath(name), "content")
}

// RemoveRemoteState deletes name's state sidecar, with the same absent-is-done
// contract as RemoveRemoteContent.
func RemoveRemoteState(name string) error {
	return removeRemoteFile(name, RemoteStatePath(name), "state")
}

// removeRemoteFile deletes one of name's remote files, treating an already
// absent file as done. kind names the file in the error, since the two callers
// differ only in which one it is.
func removeRemoteFile(name, path, kind string) error {
	if !ValidRecordName(name) {
		return fmt.Errorf("remote %q: not a valid record name", name)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remote %q: remove %s: %w", name, kind, err)
	}
	return nil
}

// writeFileAtomic replaces path with data through a temp file in the same
// directory, so a reader sees either the old bytes or the new ones. A file that
// already exists keeps its mode: replacing content must never widen or narrow
// permission a user chose for it. A new file gets perm.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
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
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// RemoteLayer is the provenance label the content of remote name carries. It
// appears in Config.ProfileLayer and in the sources a resolved value reports, so
// callers that need to tell one remote's profiles from another read it here
// rather than spelling the prefix themselves.
func RemoteLayer(name string) string {
	return remoteLayerPrefix + name
}

// loadRemoteContent parses a record's stored content the way a pull landed it.
// ok is false when there is nothing to project. A content path that cannot be
// reached at all — no file, or a component that is not a directory — is no
// content, silently: the divergence check already reports a stored file it
// cannot inspect. A file that is present but unreadable is no content either,
// but it must not vanish silently, so it comes back as an advisory. Content that
// is present but unparseable or invalid is an error, exactly as it would be for
// a user layer: resolving without the user's declared remote would hide the
// breakage.
func loadRemoteContent(name string) (content Config, ok bool, warning string, err error) {
	path := RemoteContentPath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if _, statErr := os.Stat(path); statErr != nil {
			return Config{}, false, "", nil
		}
		return Config{}, false, fmt.Sprintf("remote %q: stored content cannot be read (%v), so none of its profiles are loaded; fix its permissions or run \"enver pull %s\" to re-fetch it", name, err, name), nil
	}
	cfg, err := parseConfig(path, data)
	if err != nil {
		return Config{}, false, "", err
	}
	return cfg, true, "", nil
}

// RemoteLayers returns the stored content of every record that has some, one
// layer per record, in fold order: sorted by record name, so a later record
// folds over an earlier one and wins a profile both declare. Each content file is
// projected through Project into its record's namespace and labelled
// remote:<name>, so every entry carries the remote that supplied it, and a record
// with no content contributes no layer. The warnings cover content that is on
// disk but unreadable, which resolution had to skip, and a bare-name collision
// between two no-prefix records, where the fold makes one record's version of a
// profile unreachable.
func RemoteLayers(records map[string]RemoteRecord) ([]Layer, []string, error) {
	var layers []Layer
	var warnings []string
	bareOwner := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(records)) {
		content, ok, warning, err := loadRemoteContent(name)
		if err != nil {
			return nil, nil, err
		}
		if warning != "" {
			warnings = append(warnings, warning)
		}
		if !ok {
			continue
		}
		rec := records[name]
		projected := Project(content, name, rec.NoPrefix)
		if rec.NoPrefix {
			warnings = append(warnings, bareNameWarnings(name, projected, bareOwner)...)
		}
		layers = append(layers, Layer{Label: RemoteLayer(name), Config: projected})
	}
	return layers, warnings, nil
}

// remoteBase folds the stored content of every record that has some into the
// bottom layer of the merged view, in RemoteLayers order. The returned label
// names the topmost remote folded, the base label the next fold needs; it is
// empty when no remote contributed, leaving that label for the caller to pick.
func remoteBase(records map[string]RemoteRecord) (Config, string, []string, error) {
	layers, warnings, err := RemoteLayers(records)
	if err != nil {
		return Config{}, "", nil, err
	}
	if len(layers) == 0 {
		return Config{}, "", warnings, nil
	}
	base := layers[0].Config
	for i := 1; i < len(layers); i++ {
		base = Merge(base, layers[i].Config, layers[i-1].Label, layers[i].Label)
	}
	return base, layers[len(layers)-1].Label, warnings, nil
}

// bareNameWarnings reports the profiles a no-prefix record lands on top of, and
// records it as their new owner. The fold runs in sorted record order, so the
// later record wins; saying so is the only way a user learns that the earlier
// record's version of that profile is now unreachable.
func bareNameWarnings(name string, projected Config, bareOwner map[string]string) []string {
	var out []string
	for _, pname := range projected.ProfileNames() {
		if owner, taken := bareOwner[pname]; taken {
			out = append(out, fmt.Sprintf("remote %q: stored without a prefix, so its profile %q collides with the same bare name from remote %q; the fold runs in sorted record order, so %q wins", name, pname, owner, name))
		}
		bareOwner[pname] = name
	}
	return out
}

// orphanWarnings reports stored content no merged record declares: the file
// outlived its record, so its profiles can never load and no pull would refresh
// it. A remotes directory that cannot be listed yields no advisories — the
// per-record checks already report a directory they cannot inspect. Names that
// could never be record names are reported too: the file still sits where enver
// stores fetched content, and only removal can resolve it.
func orphanWarnings(records map[string]RemoteRecord) []string {
	entries, err := os.ReadDir(RemotesDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name, ok := strings.CutSuffix(e.Name(), ".yaml")
		if !ok {
			continue
		}
		if _, declared := records[name]; declared {
			continue
		}
		out = append(out, fmt.Sprintf("remote %q: stored content has no record declaring it, so its profiles are not loaded; remove %s or register the record to use it", name, RemoteContentPath(name)))
	}
	return out
}

// Project rewrites a remote's content into the namespace its record prefixes:
// every profile lands under name+"/", and an extends target the same file
// defines moves with it, while a target the file does not define keeps
// resolving against the user's namespace. A no-prefix record names the empty
// prefix instead: its profiles keep their bare upstream names and its own
// extends targets need no rewriting, which is the deliberate blending the mode
// opts into. The two top-level sections a remote author must not own are
// dropped: default, because which profile is default is the user's decision, and
// remotes, because a remote can never register a fetch. Control characters are
// stripped from every string the content supplies — profile names, extends
// targets, env keys, unset entries, values, comments — since they reach
// terminals, error messages and completion candidates, where an escape sequence
// or an embedded line break can rewrite what the user reads. Only value and
// comment text keeps its newlines: a multi-line value and a multi-line comment
// are structure the config and dotenv formats support, while a name carrying one
// is a rendering hazard and never a legitimate name. Secret marks ride through
// unchanged: a mark is not a secret, and a patch written over a marked remote
// value must stay masked and remain an encrypt candidate. Every projected
// profile is stamped with the remote:<name> layer. Only what the file declares is
// projected — merge artifacts (Origins, UnsetOrigins, Carried) are not carried —
// and the input is left untouched.
func Project(c Config, name string, noPrefix bool) Config {
	prefix := ""
	if !noPrefix {
		prefix = name + "/"
	}
	defined := make(map[string]bool, len(c.Profiles))
	for pname := range c.Profiles {
		defined[stripControls(pname)] = true
	}
	out := Config{
		Profiles:     make(map[string]Profile, len(c.Profiles)),
		ProfileLayer: make(map[string]string, len(c.Profiles)),
	}
	// Sorted so two upstream names that strip to one projected name resolve
	// deterministically, the later spelling winning as a later entry does.
	for _, pname := range slices.Sorted(maps.Keys(c.Profiles)) {
		p := c.Profiles[pname]
		projected := prefix + stripControls(pname)
		out.Profiles[projected] = Profile{
			Extends:  projectExtends(p.Extends, defined, prefix),
			Unset:    Unsets(stripControlsAll(p.Unset)),
			Env:      projectEnv(p.Env),
			Comments: projectComments(p.Comments),
			Marked:   projectMarks(p.Marked),
		}
		out.ProfileLayer[projected] = RemoteLayer(name)
		// The remote authored every reference its file declares, and a user
		// patch that later takes over the profile name does not inherit that
		// authorship for references it did not write itself.
		for _, target := range out.Profiles[projected].Extends {
			out.extendsOrigin(projected, target, RemoteLayer(name))
		}
	}
	return out
}

// projectExtends rewrites the targets the file itself defines to their prefixed
// form. A target the file does not define passes through, binding whatever the
// merged namespace defines under that name — a user profile or another remote's
// projection, which is how cross-remote and user-provided parents compose.
func projectExtends(list Extends, defined map[string]bool, prefix string) Extends {
	if len(list) == 0 {
		return nil
	}
	out := make(Extends, 0, len(list))
	for _, target := range list {
		target = stripControls(target)
		if defined[target] {
			target = prefix + target
		}
		out = append(out, target)
	}
	return out
}

// stripControlsAll cleans a name list, keeping nil for an empty list so a
// projected profile carries exactly the sections its file did.
func stripControlsAll(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, stripControls(s))
	}
	return out
}

// projectEnv rebuilds an env map under its cleaned names: a key loses every
// control character, since a name is an identifier, while a value keeps its
// newlines, since a value is data. Sorted so two keys that clean to one name
// resolve deterministically instead of by write order.
func projectEnv(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out[stripControls(k)] = stripControlsKeepLines(m[k])
	}
	return out
}

// projectComments rebuilds a comment map: the key names the env key it
// annotates and loses every control character, while the text keeps its lines.
func projectComments(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out[stripControls(k)] = stripControlsKeepLines(m[k])
	}
	return out
}

// projectMarks rebuilds a mark set under its cleaned keys, keeping the nil
// contract: no marks is nil, never an empty map.
func projectMarks(m map[string]bool) map[string]bool {
	var out map[string]bool
	for _, k := range slices.Sorted(maps.Keys(m)) {
		if !m[k] {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		out[stripControls(k)] = true
	}
	return out
}

// stripControls removes every control character from s, newlines included. A
// remote is an untrusted author, and its names and targets reach terminals,
// error messages and completion candidates, where an escape sequence or an
// embedded line break can rewrite what the user reads.
func stripControls(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// stripControlsKeepLines is stripControls for text that is data rather than a
// name: newlines survive, because a multi-line value and a multi-line comment
// are structure the config and dotenv formats support.
func stripControlsKeepLines(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// remoteWarnings reports the merged records whose stored content disagrees with
// the source the record points at: content fetched from another URL or ref, or
// content with no recorded provenance at all. Resolution stays offline and keeps
// serving what is on disk — the warning is how a mismatch stops being silent.
// A record that pins no ref also reports the commit its content came from: with
// no fixed ref, that sha is the only thing the stale content can be held to.
// A record with no content yet has nothing to disagree with and contributes
// nothing.
func remoteWarnings(records map[string]RemoteRecord) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(records)) {
		if _, err := os.Stat(RemoteContentPath(name)); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			out = append(out, fmt.Sprintf("remote %q: stored content cannot be inspected (%v); run \"enver pull %s\" to re-fetch it", name, err, name))
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
		warning := fmt.Sprintf("remote %q: content was fetched from %s, but the record now points at %s", name, sourceLabel(st.URL, st.Ref), sourceLabel(url, ref))
		if source.Unpinned(ref) && st.Commit != "" {
			warning += fmt.Sprintf("; the record pins no ref, so that source can serve different content at any time and the stored content is commit %s", st.Commit)
		}
		out = append(out, warning+fmt.Sprintf("; run \"enver pull %s\" to re-sync", name))
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
