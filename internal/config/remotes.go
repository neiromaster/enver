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

// ValidRecordPath reports whether path is a legal remote record identity: a
// non-empty slash path whose every segment is a valid record name. The path is
// what maps a record to its files under the remotes dir, so an empty segment
// would name no directory and a segment outside the record-name rule could
// climb out of that directory.
func ValidRecordPath(path string) bool {
	for _, segment := range strings.Split(path, "/") {
		if !ValidRecordName(segment) {
			return false
		}
	}
	return true
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
// sidecar, one <name>.yaml and one <name>.meta per record, where a record name
// may be a slash path whose segments become nested directories. A var so tests
// and callers embedding enver can redirect it.
var RemotesDir = func() string {
	return filepath.Join(xdg.ConfigHome(), "enver", "remotes")
}

// RemoteContentPath is the file holding name's pristine pulled content. A path
// name becomes a directory chain, so the record ollama/claude mirrors to
// remotes/ollama/claude.yaml.
func RemoteContentPath(name string) string {
	return filepath.Join(RemotesDir(), filepath.FromSlash(name)) + ".yaml"
}

// RemoteStatePath is the sidecar recording what name's content was fetched from.
// Callers that need to inspect or remove the file use this rather than build the
// name themselves, so the layout stays in one place.
func RemoteStatePath(name string) string {
	return filepath.Join(RemotesDir(), filepath.FromSlash(name)) + ".meta"
}

// FileStoreDir is where the stored content of the records a standalone config
// declares lives: next to the file, under .enver, named after the file, so a
// file checked into a repo carries its own remote store instead of writing into
// the user's machine-wide one, and two files in one directory keep their stores
// apart. Callers point RemotesDir here before a standalone load or pull.
func FileStoreDir(file string) string {
	return filepath.Join(filepath.Dir(file), ".enver", filepath.Base(file))
}

// PullHintFlags holds the flag text a pull hint spells after `enver pull`: the
// invocation's own flags, set by the caller that knows how the command was run,
// so a hint suggests the same view the user is in. Empty in the default view.
var PullHintFlags string

// PullCommand renders the pull line a hint shows, flags first so the suggested
// line reproduces the invocation the hint came from, then the arguments the
// hint names.
func PullCommand(args ...string) string {
	line := "enver pull"
	if PullHintFlags != "" {
		line += " " + PullHintFlags
	}
	if len(args) > 0 {
		line += " " + strings.Join(args, " ")
	}
	return line
}

// ReadRemoteState reads the fetch provenance recorded for name. ok is false when
// no state can be trusted: the remote was never pulled, the sidecar is
// unreadable or records no source URL, or name falls outside the record-path
// rule. All of those leave the content's provenance unknown, which the load-time
// check reports as content due for a re-pull rather than as content whose source
// is verified.
func ReadRemoteState(name string) (RemoteState, bool) {
	if !ValidRecordPath(name) {
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
	if !ValidRecordPath(name) {
		return fmt.Errorf("remote %q: not a valid record path", name)
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
	if !ValidRecordPath(name) {
		return fmt.Errorf("remote %q: not a valid record path", name)
	}
	if err := writeFileAtomic(RemoteContentPath(name), data, 0o600); err != nil {
		return fmt.Errorf("remote %q: write content: %w", name, err)
	}
	return nil
}

// RemoveRemoteContent deletes name's stored content and, with it, the directory
// its name maps to: a record's path created that directory for its files, and
// for a path-shaped name it is the subtree its child records live in, so
// dropping the record drops it whole. Content already absent is not an error:
// the call reports that no content is left, which is what a pull dropping a
// record's last declaration wants.
func RemoveRemoteContent(name string) error {
	if err := removeRemoteFile(name, RemoteContentPath(name), "content"); err != nil {
		return err
	}
	dir := filepath.Join(RemotesDir(), filepath.FromSlash(name))
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remote %q: remove content directory: %w", name, err)
	}
	return nil
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
	if !ValidRecordPath(name) {
		return fmt.Errorf("remote %q: not a valid record path", name)
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

// MaxRemoteDepth caps how many records one remote subtree may stack. Each level
// is another file parsed and folded at every load, and composition walks a
// contentOf that need not be finite, so the cap is what keeps a hostile or
// hand-built tree from turning resolution unbounded. The pull side guards the
// same cap when it walks a tree.
const MaxRemoteDepth = 16

// ComposeTree loads the stored subtree of one top-level record: the record's
// own content first, then every child its content's remotes: section declares,
// each recursively, so the layers fold parent-first with siblings in sorted
// record order. contentOf supplies a record's stored bytes and reports absence,
// which keeps composition independent of where the content lives. See
// composeTree for the full composition contract.
func ComposeTree(root string, rootNoPrefix bool, contentOf func(path string) ([]byte, bool)) ([]Layer, []string, error) {
	return composeTree(root, recordPrefix(root, rootNoPrefix), contentOf, map[string]string{}, nil)
}

// composeTree is ComposeTree with the fold state threaded through the
// recursion: prefix is the namespace this record's profiles land under, owner
// maps each loaded profile name to the record that currently owns it, and chain
// is the record path from the subtree root, for the depth cap and the re-pull
// hint. A record with no content contributes nothing — silently for the root,
// whose absence the divergence check already reports, and as an advisory for a
// declared child, whose parent promised content that is not there. Content that
// is present but unparseable or invalid is an error, exactly as it would be for
// a user layer: resolving without the user's declared remote would hide the
// breakage. A duplicate profile name warns, naming the record that wins it.
func composeTree(path, prefix string, contentOf func(path string) ([]byte, bool), owner map[string]string, chain []string) ([]Layer, []string, error) {
	chain = append(slices.Clone(chain), path)
	if len(chain) > MaxRemoteDepth {
		return nil, nil, fmt.Errorf("remote %q: nested remotes stack deeper than the %d record cap: %s", path, MaxRemoteDepth, strings.Join(chain, " -> "))
	}
	data, ok := contentOf(path)
	if !ok {
		if len(chain) == 1 {
			return nil, nil, nil
		}
		return nil, []string{fmt.Sprintf("remote %q: declared by remote %q but no stored content could be loaded, so its profiles are not loaded; run %q to fetch the subtree", path, chain[len(chain)-2], PullCommand(chain[0]))}, nil
	}
	// The wrap names the record, so the parse step labels the content by its role
	// rather than spelling the record path a second time back-to-back.
	content, err := parseConfig("stored content", data)
	if err != nil {
		return nil, nil, fmt.Errorf("remote %q: %w", path, err)
	}
	projected, err := Project(content, prefix, prefix == "")
	if err != nil {
		return nil, nil, fmt.Errorf("remote %q: %w", path, err)
	}
	restampLayer(path, projected)
	warnings := duplicateNameWarnings(path, projected, owner)
	layers := []Layer{{Label: RemoteLayer(path), Config: projected}}
	for _, name := range slices.Sorted(maps.Keys(content.Remotes)) {
		childPrefix := prefix
		if !content.Remotes[name].NoPrefix {
			childPrefix = name
			if prefix != "" {
				childPrefix = prefix + "/" + name
			}
		}
		sub, subWarnings, err := composeTree(path+"/"+name, childPrefix, contentOf, owner, chain)
		if err != nil {
			return nil, nil, err
		}
		layers = append(layers, sub...)
		warnings = append(warnings, subWarnings...)
	}
	return layers, warnings, nil
}

// recordPrefix is the namespace a record's own profiles land under: its path,
// or nothing under a no-prefix record.
func recordPrefix(path string, noPrefix bool) string {
	if noPrefix {
		return ""
	}
	return path
}

// restampLayer relabels a projection after its record path. Project labels by
// the name it was handed, and a composed prefix can be shorter than the record
// — a no-prefix child under its parent, a no-prefix root — while provenance
// must still name the record the content actually came from.
func restampLayer(path string, projected Config) {
	for pname := range projected.ProfileLayer {
		projected.ProfileLayer[pname] = RemoteLayer(path)
	}
	for _, targets := range projected.ExtendsOrigins {
		for target := range targets {
			targets[target] = RemoteLayer(path)
		}
	}
}

// duplicateNameWarnings reports the profile names a record lands on top of in
// the fold, recording each name's owner as the fold passes. The fold runs
// parent-first then in sorted record order, so the later record wins; saying so
// is the only way a user learns that the earlier record's version of that
// profile is now unreachable.
func duplicateNameWarnings(path string, projected Config, owner map[string]string) []string {
	var out []string
	for _, pname := range projected.ProfileNames() {
		if prev, taken := owner[pname]; taken {
			out = append(out, fmt.Sprintf("remote %q: its profile %q collides with the same profile name already loaded from remote %q; the fold runs parent-first then in sorted record order, so %q wins", path, pname, prev, path))
		}
		owner[pname] = path
	}
	return out
}

// RemoteLayers returns the stored content of every record that has some,
// composed: each top-level record contributes its whole subtree via ComposeTree,
// records in sorted order, so a later record wins a profile an earlier one also
// declares. Every layer is labelled remote:<record path>, so every entry carries
// the record that supplied it, and a record with no content contributes no
// layer. The warnings cover content that is on disk but unreadable, which
// resolution had to skip, children declared but never pulled, and duplicate
// profile names, where the fold makes one record's version of a profile
// unreachable.
func RemoteLayers(records map[string]RemoteRecord) ([]Layer, []string, error) {
	var layers []Layer
	var warnings []string
	owner := map[string]string{}
	read := func(path string) ([]byte, bool) {
		data, err := os.ReadFile(RemoteContentPath(path))
		if err != nil {
			if _, statErr := os.Stat(RemoteContentPath(path)); statErr != nil {
				return nil, false
			}
			warnings = append(warnings, fmt.Sprintf("remote %q: stored content cannot be read (%v), so none of its profiles are loaded; fix its permissions or run %q to re-fetch it", path, err, PullCommand(path)))
			return nil, false
		}
		return data, true
	}
	for _, name := range slices.Sorted(maps.Keys(records)) {
		sub, subWarnings, err := composeTree(name, recordPrefix(name, records[name].NoPrefix), read, owner, nil)
		if err != nil {
			return nil, nil, err
		}
		layers = append(layers, sub...)
		warnings = append(warnings, subWarnings...)
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

// reachableRecords collects the record paths the config's declarations reach:
// every declared top-level record plus every child each reachable record's
// stored content declares, recursively. It reads declarations only — the plain
// parse every stored file gets — so a record with no stored content reaches
// nothing further, and content the load cannot parse ends the walk there,
// where composition itself refuses the file.
func reachableRecords(records map[string]RemoteRecord) map[string]bool {
	reachable := make(map[string]bool, len(records))
	var walk func(path string)
	walk = func(path string) {
		if reachable[path] {
			return
		}
		reachable[path] = true
		data, err := os.ReadFile(RemoteContentPath(path))
		if err != nil {
			return
		}
		content, err := parseConfig("stored content", data)
		if err != nil {
			return
		}
		for _, name := range slices.Sorted(maps.Keys(content.Remotes)) {
			walk(path + "/" + name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(records)) {
		walk(name)
	}
	return reachable
}

// orphanWarnings reports stored entries no declared record reaches: the files
// outlived their records, so their profiles can never load and no pull would
// refresh them. Reachability follows stored declarations, so a subtree left
// behind by a renamed or removed record is reported by its root path, once —
// the directory tells the whole story, and the files under it add nothing.
// Content and sidecar entries that could never be record paths are reported
// too: they still sit where enver stores fetched content, and only removal can
// resolve them. A remotes directory that cannot be walked yields no advisories
// — the per-record checks already report content they cannot inspect.
func orphanWarnings(records map[string]RemoteRecord) []string {
	reachable := reachableRecords(records)
	reported := map[string]bool{}
	var out []string
	_ = filepath.Walk(RemotesDir(), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(RemotesDir(), path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		if info.IsDir() {
			if reachable[name] || reported[name] {
				return nil
			}
			reported[name] = true
			out = append(out, fmt.Sprintf("remote %q: stored content has no record declaring it, so the whole subtree is orphaned and its profiles are not loaded; remove %s or register the record to use it", name, path))
			return filepath.SkipDir
		}
		recordPath, ok := strings.CutSuffix(name, ".yaml")
		if !ok {
			recordPath, ok = strings.CutSuffix(name, ".meta")
		}
		if !ok || reachable[recordPath] || reported[recordPath] {
			return nil
		}
		reported[recordPath] = true
		out = append(out, fmt.Sprintf("remote %q: stored content has no record declaring it, so its profiles are not loaded; remove %s or register the record to use it", recordPath, path))
		return nil
	})
	return out
}

// InNamespace reports whether target resolves inside the namespace a fetched
// remote file defines: a bare name one of the file's own profiles defines, or a
// name whose first /-separated segment names a record in the file's own
// remotes: section. Everything else — a user profile, another record's
// projection, the record's own pulled name — is out of namespace. The gate
// refuses such a target and the projection errors on it, so the rule is stated
// once here and both sides enforce the same set. The gate passes the raw file
// names; the projection passes its control-stripped ones, since stripping is
// part of how it reads what the file declares.
func InNamespace(target string, defined map[string]bool, children map[string]RemoteRecord) bool {
	name, _, hasSlash := strings.Cut(target, "/")
	if !hasSlash {
		return defined[target]
	}
	_, ok := children[name]
	return ok
}

// Project rewrites a remote's content into the namespace its record prefixes:
// every profile lands under name+"/", and every extends target inside the
// file's own namespace moves with it — a bare target the file defines, or a
// target under a record the file's own remotes: section declares. A target
// outside that namespace is an error: the file resolves only what it declares,
// never whatever the pulling user happens to have. A no-prefix record names the
// empty prefix instead: its profiles keep their bare upstream names, so
// in-namespace targets already carry their projected form and pass through
// unchanged. The two top-level sections a remote author must not own are
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
func Project(c Config, name string, noPrefix bool) (Config, error) {
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
		ext, err := projectExtends(p.Extends, defined, c.Remotes, prefix)
		if err != nil {
			return Config{}, fmt.Errorf("profile %s: %w", projected, err)
		}
		out.Profiles[projected] = Profile{
			Extends:  ext,
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
	return out, nil
}

// projectExtends rewrites every target the file's namespace defines to its
// prefixed form and refuses the rest: a remote file resolves its own profiles
// and the records its own remotes: section declares, so a target the user or
// another record owns has no meaning inside this file.
func projectExtends(list Extends, defined map[string]bool, children map[string]RemoteRecord, prefix string) (Extends, error) {
	if len(list) == 0 {
		return nil, nil
	}
	out := make(Extends, 0, len(list))
	for _, target := range list {
		target = stripControls(target)
		if !InNamespace(target, defined, children) {
			return nil, fmt.Errorf("extends %q, which is outside this file: a remote file resolves only its own profiles and the records in its own remotes: section", target)
		}
		out = append(out, prefix+target)
	}
	return out, nil
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

// remoteWarnings reports the records whose stored content disagrees with the
// source its declaration points at: content fetched from another URL or ref,
// or content with no recorded provenance at all. The declaration is the
// config's remotes: section for a top-level record and the parent's stored
// content for a nested one, and the check recurses through stored
// declarations, so a child its parent re-pointed is named by its own path.
// Resolution stays offline and keeps serving what is on disk — the warning is
// how a mismatch stops being silent. A record that pins no ref also reports
// the commit its content came from: with no fixed ref, that sha is the only
// thing the stale content can be held to. A record with no content yet has
// nothing to disagree with and contributes nothing.
func remoteWarnings(records map[string]RemoteRecord) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(records)) {
		out = append(out, divergenceWarnings(name, records[name], name)...)
	}
	return out
}

// divergenceWarnings checks one record's stored content against the source its
// declaration resolves to, then recurses into the children that content
// declares, each checked against the declaration its parent carries. pullTarget
// is the top-level record the re-pull hints name: pull fetches a subtree whole,
// never by a nested path. The sidecar comparison needs no content bytes, so it
// runs on whatever a stat can see; only the recursion into declared children
// needs the file itself, and unreadable or unparseable content ends the walk
// there, where the read seam and composition report the file.
func divergenceWarnings(path string, rec RemoteRecord, pullTarget string) []string {
	if _, err := os.Stat(RemoteContentPath(path)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []string{fmt.Sprintf("remote %q: stored content cannot be inspected (%v); run %q to re-fetch it", path, err, PullCommand(pullTarget))}
	}
	out := stateDivergenceWarning(path, rec, pullTarget)
	data, err := os.ReadFile(RemoteContentPath(path))
	if err != nil {
		return out
	}
	content, err := parseConfig("stored content", data)
	if err != nil {
		return out
	}
	for _, name := range slices.Sorted(maps.Keys(content.Remotes)) {
		out = append(out, divergenceWarnings(path+"/"+name, content.Remotes[name], pullTarget)...)
	}
	return out
}

// stateDivergenceWarning compares the sidecar stored for path against the
// source its declaration resolves to, in the voice the top-level check has
// always used, with the unpinned-commit note for a record whose source can
// serve different content at any time.
func stateDivergenceWarning(path string, rec RemoteRecord, pullTarget string) []string {
	url, ref, err := source.ResolveRecord(rec.URL, rec.Ref)
	if err != nil {
		return []string{fmt.Sprintf("remote %q: the record's source is unusable (%v); the stored content cannot be verified, so run %q once the record is fixed", path, err, PullCommand(pullTarget))}
	}
	st, ok := ReadRemoteState(path)
	if !ok {
		return []string{fmt.Sprintf("remote %q: content has no pull state to compare, and the record points at %s; run %q to re-fetch it", path, sourceLabel(url, ref), PullCommand(pullTarget))}
	}
	if url == st.URL && ref == st.Ref {
		return nil
	}
	warning := fmt.Sprintf("remote %q: content was fetched from %s, but the record now points at %s", path, sourceLabel(st.URL, st.Ref), sourceLabel(url, ref))
	if source.Unpinned(ref) && st.Commit != "" {
		warning += fmt.Sprintf("; the record pins no ref, so that source can serve different content at any time and the stored content is commit %s", st.Commit)
	}
	return []string{warning + fmt.Sprintf("; run %q to re-sync", PullCommand(pullTarget))}
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
