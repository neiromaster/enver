package app

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/remote"
	"gopkg.in/yaml.v3"
)

// PullOptions is one pull run: the layer it writes, the view it judges fetched
// content against, and the seams tests inject.
type PullOptions struct {
	// TargetIsGlobal selects the layer a registration writes: the global config
	// by default, the project's .enver.yaml under --local.
	TargetIsGlobal bool
	// NoPrefix registers the remote with bare upstream names. It is a
	// registration mode, not an update one: a stored record owns its mode.
	NoPrefix bool
	// Rm makes Pull behave as PullRemove, so one option set serves the CLI.
	Rm bool
	// Force accepts a takeover without asking.
	Force bool
	// Merged is the full merged view: every layer plus the projection of every
	// pulled remote. The gate reads it to classify what a fetched extends target
	// binds and to see collisions with any remote or user profile.
	Merged config.Config
	// Target and TargetPath are the file the pull writes and its parsed view;
	// Other and OtherPath are the layer it leaves alone. A registration writes
	// the record to Target, while the takeover check judges the effective record
	// in Merged, which is whichever layer wins.
	Target, Other         config.Config
	TargetPath, OtherPath string
	// LayerPinned marks an explicit -g/--local choice, so a removal targets that
	// layer instead of the closest declaration.
	LayerPinned bool
	// OtherHidden marks the other layer as out of view, as --no-local puts the
	// project file for a removal: the write layer is then the only declaration a
	// removal may target, while the hidden layer's record still protects stored
	// content from deletion.
	OtherHidden bool
	// Confirm asks the user about a takeover; nil on a non-interactive path,
	// where Force is the only way through. A nil error with ok=false aborts.
	Confirm func(msg string, def bool) (bool, error)
	// Fetch downloads a resolved source; nil means remote.Fetch. Tests inject it
	// to make a pull hermetic and to fail one on purpose.
	Fetch func(url, ref string) ([]byte, string, error)
}

// Pull registers a source under name in the target layer and fetches it. A name
// the target already declares is re-registered: the summary prints the source it
// pointed at and the one it points at now, and when user-layer patch profiles
// under the prefix exist, a changed source or pull mode is a takeover, shown and
// confirmed first — either one changes which content the user's credentials
// compose with, which is exactly the moment that must not happen silently.
//
// Nothing is written until the fetch and the gate both accept the content: a
// refused upstream leaves the record, the stored content, and the sidecar as
// they were.
func Pull(rawSource, name string, opts PullOptions) (string, error) {
	if opts.Rm {
		return PullRemove(name, opts)
	}
	body, ref, err := remote.SplitRef(rawSource)
	if err != nil {
		return "", err
	}
	rec := config.RemoteRecord{URL: body, Ref: ref, NoPrefix: opts.NoPrefix}
	if err := checkNewRecord(name, rec, opts); err != nil {
		return "", err
	}
	changes, proceed, err := confirmTakeover(name, rec, opts)
	if err != nil {
		return "", err
	}
	if !proceed {
		return "", nil
	}
	res, err := pullRecord(name, rec, opts)
	if err != nil {
		return "", err
	}
	res.changes = changes
	if err := writeRecord(name, rec, opts); err != nil {
		return "", err
	}
	return res.render(), nil
}

// PullUpdate re-fetches one registered remote from the source its effective
// record names. The record is not rewritten: an update refreshes content, while
// the source and the pull mode stay whatever the config declares. A record whose
// stored content the sidecar says came from another source is a takeover when
// patch profiles sit under its prefix, exactly as a re-registration is, and is
// confirmed before anything is fetched.
func PullUpdate(name string, opts PullOptions) (string, error) {
	rec, ok := opts.Merged.Remotes[name]
	if !ok {
		return "", notRegistered(name)
	}
	if opts.NoPrefix {
		return "", fmt.Errorf("--no-prefix applies only when registering a source; remote %q keeps the mode stored in its record", name)
	}
	changes, proceed, err := confirmRefetch(name, rec, opts)
	if err != nil {
		return "", err
	}
	if !proceed {
		return "", nil
	}
	res, err := pullRecord(name, rec, opts)
	if err != nil {
		return "", err
	}
	res.changes = changes
	return res.render(), nil
}

// PullAll re-fetches every remote the merged view registers, in sorted record
// order. One failing remote does not stop the rest: every record is attempted,
// each outcome is reported, and the run fails if any of them did. Each record
// passes the same takeover gate a single re-fetch does before it is fetched.
func PullAll(opts PullOptions) (string, error) {
	var b strings.Builder
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(opts.Merged.Remotes)) {
		rec := opts.Merged.Remotes[name]
		changes, proceed, err := confirmRefetch(name, rec, opts)
		if err != nil {
			fmt.Fprintf(&b, "\n✗ %v\n", err)
			errs = append(errs, err)
			continue
		}
		if !proceed {
			fmt.Fprintf(&b, "\n- remote %q left unchanged: takeover declined\n", name)
			continue
		}
		res, err := pullRecord(name, rec, opts)
		if err != nil {
			fmt.Fprintf(&b, "\n✗ %v\n", err)
			errs = append(errs, err)
			continue
		}
		res.changes = changes
		b.WriteString(res.render())
	}
	return b.String(), errors.Join(errs...)
}

// PullRemove removes one declaration of name and reports what is left. An
// explicit layer targets that layer; otherwise the closest declaration goes —
// the one that controls the source right now. A layer marked OtherHidden takes no
// part in that choice: it is the write layer or nothing. Stored content is
// deleted only when no declaration anywhere still needs it, hidden layers
// included, since deleting content a surviving record needs would blank every
// profile under the prefix. The summary names a surviving declaration and its
// source, and lists user-layer profiles under the prefix, which survive as
// standalone profiles but lose every inherited key.
func PullRemove(name string, opts PullOptions) (string, error) {
	if !config.ValidRecordName(name) {
		return "", fmt.Errorf("remote name %q is not a valid record name", name)
	}
	target := remoteLayerState{path: opts.TargetPath, cfg: opts.Target, global: opts.TargetIsGlobal}
	other := remoteLayerState{path: opts.OtherPath, cfg: opts.Other, global: !opts.TargetIsGlobal}
	// The closest declaration is the local one when it exists: it is the layer
	// that controls the source right now, whichever layer the write target is.
	closest, farthest := target, other
	if target.global && !other.global {
		closest, farthest = other, target
	}
	from, into := target, other
	switch {
	case opts.LayerPinned:
		if !declaresRemote(from.cfg, name) {
			if declaresRemote(into.cfg, name) {
				return "", fmt.Errorf("remote %q is not declared in the %s; it is declared in the %s — run without the layer flag to target it", name, layerName(from.global), layerName(into.global))
			}
			return "", fmt.Errorf("remote %q is not declared in the %s", name, layerName(from.global))
		}
	case opts.OtherHidden:
		if !declaresRemote(target.cfg, name) {
			return "", notRegistered(name)
		}
	case declaresRemote(closest.cfg, name):
		from, into = closest, farthest
	case declaresRemote(farthest.cfg, name):
		from, into = farthest, closest
	default:
		return "", notRegistered(name)
	}
	next := maps.Clone(from.cfg.Remotes)
	delete(next, name)
	if err := config.WriteRemotes(from.path, next); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n✓ removed remote %q from the %s\n", name, layerName(from.global))
	if rec, ok := into.cfg.Remotes[name]; ok {
		fmt.Fprintf(&b, "  remote %q is still declared in the %s and now resolves to %s; run enver pull %s to refresh its content\n",
			name, layerName(into.global), recordSource(rec), name)
		return b.String(), nil
	}
	if err := config.RemoveRemoteContent(name); err != nil {
		return "", err
	}
	if err := config.RemoveRemoteState(name); err != nil {
		return "", err
	}
	b.WriteString("  its stored content and pull state are deleted\n")
	for _, patch := range patchProfiles(name, opts.Target, opts.Other) {
		fmt.Fprintf(&b, "  profile %q no longer inherits from remote %q; it keeps its own values only\n", patch, name)
	}
	return b.String(), nil
}

func notRegistered(name string) error {
	return fmt.Errorf("remote %q is not registered; register it with enver pull <source> --as %s", name, name)
}

// remoteLayerState is one config file a pull can read or write, named the way
// the summary and errors refer to it.
type remoteLayerState struct {
	path   string
	cfg    config.Config
	global bool
}

func layerName(global bool) string {
	if global {
		return "global config"
	}
	return "local .enver.yaml"
}

func declaresRemote(cfg config.Config, name string) bool {
	_, ok := cfg.Remotes[name]
	return ok
}

// checkNewRecord refuses a registration the config could not survive: an
// unusable name or source, a name that differs only in case from another record
// (the two would name one file on a case-insensitive filesystem), and a
// registration in the global layer that the local layer already shadows, which
// would fetch content the effective record never asks for.
//
// The validation covers the whole prospective section, not just the new record:
// WriteRemotes rewrites the section and validates all of it, so a sibling record
// that is already unusable would otherwise fail the write after the content and
// the sidecar had landed, leaving artifacts for a record that never existed.
func checkNewRecord(name string, rec config.RemoteRecord, opts PullOptions) error {
	next := maps.Clone(opts.Target.Remotes)
	if next == nil {
		next = map[string]config.RemoteRecord{}
	}
	next[name] = rec
	if err := config.ValidateRemotes(next); err != nil {
		return fmt.Errorf("the remotes section of the %s would not survive a rewrite: %w", layerName(opts.TargetIsGlobal), err)
	}
	for _, existing := range slices.Sorted(maps.Keys(opts.Merged.Remotes)) {
		if existing != name && strings.EqualFold(existing, name) {
			return fmt.Errorf("remote name %q differs only in case from the record %q already in your config; record names become file names, and the filesystem may not tell them apart", name, existing)
		}
	}
	if _, inTarget := opts.Target.Remotes[name]; !inTarget && declaresRemote(opts.Other, name) && opts.TargetIsGlobal {
		return fmt.Errorf("remote %q is declared in the local .enver.yaml, which overrides the global layer; register it with --local", name)
	}
	return nil
}

// confirmTakeover decides whether registering rec may proceed, and returns the
// summary lines describing what changed. A re-registration that changes nothing
// is silent; one that changes the source or the pull mode says so, and when
// user-layer patch profiles sit under the prefix, either change is a takeover:
// it is confirmed (or accepted under Force) before anything is fetched, so a
// declined takeover costs no network and no write. A mode change is as invasive
// as a re-point, since the patches stop meeting the profiles they were written
// against.
func confirmTakeover(name string, rec config.RemoteRecord, opts PullOptions) (changes []string, proceed bool, err error) {
	effective, declared := opts.Merged.Remotes[name]
	if !declared {
		return nil, true, nil
	}
	before := storedSource(name, effective)
	sourceChanged := !sameSource(before, rec)
	if sourceChanged {
		changes = append(changes, fmt.Sprintf("source changed from %s to %s", recordSource(before), recordSource(rec)))
	}
	if before.NoPrefix != rec.NoPrefix {
		changes = append(changes, fmt.Sprintf("pull mode changed from %s to %s", pullMode(before.NoPrefix), pullMode(rec.NoPrefix)))
	}
	if len(changes) == 0 {
		return nil, true, nil
	}
	patches := patchProfiles(name, opts.Target, opts.Other)
	if len(patches) == 0 {
		return changes, true, nil
	}
	warning := takeoverWarning(name, before, rec, patches, sourceChanged)
	proceed, err = gateTakeover(warning, opts)
	if err != nil {
		return nil, false, err
	}
	if !proceed {
		return nil, false, nil
	}
	return append(changes, warning), true, nil
}

// confirmRefetch decides whether re-fetching rec may proceed, and returns the
// summary lines describing why it asked. The stored content is the thing being
// replaced, so the comparison keys off the sidecar, which records the source it
// was actually fetched from: the effective record alone cannot tell a re-fetch
// from a first pull into a re-declared name, and after a project re-declares a
// global record it already names the new source. When the two disagree and
// user-layer patch profiles sit under the prefix, the fetch is a takeover and is
// confirmed (or accepted under Force) before any network call. With no sidecar
// there is nothing to disagree with, so the fetch proceeds: unverifiable
// provenance is the load path's advisory, not a takeover.
func confirmRefetch(name string, rec config.RemoteRecord, opts PullOptions) (changes []string, proceed bool, err error) {
	url, ref, resolveErr := remote.ResolveRecord(rec)
	if resolveErr != nil {
		return nil, true, nil // pullRecord reports the unusable source
	}
	st, ok := config.ReadRemoteState(name)
	if !ok || (st.URL == url && st.Ref == ref) {
		return nil, true, nil
	}
	stored := config.RemoteRecord{URL: st.URL, Ref: st.Ref}
	changes = append(changes, fmt.Sprintf("stored content came from %s, but the record now points at %s", recordSource(stored), recordSource(rec)))
	patches := patchProfiles(name, opts.Target, opts.Other)
	if len(patches) == 0 {
		return changes, true, nil
	}
	warning := takeoverWarning(name, stored, rec, patches, true)
	proceed, err = gateTakeover(warning, opts)
	if err != nil {
		return nil, false, err
	}
	if !proceed {
		return nil, false, nil
	}
	return append(changes, warning), true, nil
}

// gateTakeover asks the user about a takeover warning: --force accepts without
// asking and keeps the warning in the summary, a nil Confirm is the
// non-interactive path and fails with the --force hint, and a declined prompt
// reports proceed=false without an error.
func gateTakeover(warning string, opts PullOptions) (bool, error) {
	if opts.Force {
		return true, nil
	}
	if opts.Confirm == nil {
		return false, fmt.Errorf("%s; pass --force to accept without confirming", warning)
	}
	ok, err := opts.Confirm(warning, false)
	if err != nil {
		return false, fmt.Errorf("%w; pass --force to accept without confirming", err)
	}
	return ok, nil
}

// storedSource is where name's stored content came from as far as the sidecar
// attests: the record itself when the record still resolves to what the sidecar
// recorded, so the summary keeps the spelling the user wrote, or the sidecar's
// source when the merged view already moved on. The sidecar is the only
// trustworthy record of the content's origin — after a re-declaration the merged
// record names the new source, so comparing against it would call a poisoned
// record unchanged. A record with no sidecar is its own prior source.
func storedSource(name string, rec config.RemoteRecord) config.RemoteRecord {
	st, ok := config.ReadRemoteState(name)
	if !ok {
		return rec
	}
	url, ref, err := remote.ResolveRecord(rec)
	if err == nil && st.URL == url && st.Ref == ref {
		return rec
	}
	return config.RemoteRecord{URL: st.URL, Ref: st.Ref, NoPrefix: rec.NoPrefix}
}

// takeoverWarning names what the record now does differently, the profiles whose
// meaning the change touches, and what the user is being asked to decide. A
// mode change leaves the source alone, so its consequence is spelled out for
// what actually happens in that direction: a prefix dropped means the patches
// stop meeting the profiles they were written against, a prefix added means they
// start meeting profiles they did not patch before.
func takeoverWarning(name string, before, after config.RemoteRecord, patches []string, sourceChanged bool) string {
	list := strings.Join(patches, ", ")
	what := fmt.Sprintf("now points at %s instead of %s", recordSource(after), recordSource(before))
	effect := list + " will now compose with content from that source"
	if !sourceChanged {
		what = fmt.Sprintf("now stores %s instead of %s", pullMode(after.NoPrefix), pullMode(before.NoPrefix))
		effect = list + " will now patch the remote's profiles under the prefix they are stored with"
		if after.NoPrefix {
			effect = list + " will no longer patch the remote's profiles under the names they use"
		}
	}
	return fmt.Sprintf("remote %q %s; %s, so review before trusting it with your credentials", name, what, effect)
}

func pullMode(noPrefix bool) string {
	if noPrefix {
		return "bare names"
	}
	return "prefixed names"
}

// sameSource reports whether two records resolve to the same fetch. A record
// whose source the resolver cannot read is never the same as another: a pull
// over it repairs the record rather than assume what it meant.
func sameSource(a, b config.RemoteRecord) bool {
	aURL, aRef, aErr := remote.ResolveRecord(a)
	bURL, bRef, bErr := remote.ResolveRecord(b)
	if aErr != nil || bErr != nil {
		return a == b
	}
	return aURL == bURL && aRef == bRef
}

// patchProfiles lists the user-layer profiles under name's prefix, the ones a
// re-point or a removal changes the meaning of.
func patchProfiles(name string, layers ...config.Config) []string {
	prefix := name + "/"
	seen := map[string]struct{}{}
	var out []string
	for _, cfg := range layers {
		for _, profile := range cfg.ProfileNames() {
			if !strings.HasPrefix(profile, prefix) {
				continue
			}
			if _, ok := seen[profile]; ok {
				continue
			}
			seen[profile] = struct{}{}
			out = append(out, profile)
		}
	}
	slices.Sort(out)
	return out
}

// writeRecord stores rec in the target layer, leaving the section's other
// records as they stand. An unchanged record is not rewritten, so a re-pull by
// source never reformats a hand-authored section.
func writeRecord(name string, rec config.RemoteRecord, opts PullOptions) error {
	if existing, ok := opts.Target.Remotes[name]; ok && existing == rec {
		return nil
	}
	next := maps.Clone(opts.Target.Remotes)
	if next == nil {
		next = map[string]config.RemoteRecord{}
	}
	next[name] = rec
	return config.WriteRemotes(opts.TargetPath, next)
}

// pullResult is what one record's pull reports.
type pullResult struct {
	name     string
	source   string
	profiles []string
	commit   string
	changes  []string
	warnings []string
}

func (r pullResult) render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n✓ remote %q ← %s\n", r.name, r.source)
	for _, change := range r.changes {
		fmt.Fprintf(&b, "  %s\n", change)
	}
	if len(r.profiles) > 0 {
		fmt.Fprintf(&b, "  profiles: %s\n", strings.Join(r.profiles, ", "))
	}
	if r.commit != "" {
		fmt.Fprintf(&b, "  commit: %s\n", r.commit)
	}
	for _, warning := range r.warnings {
		fmt.Fprintf(&b, "  warning: %s\n", warning)
	}
	return b.String()
}

// pullRecord fetches and gates one record's content, then stores it and its
// provenance. The content and sidecar are written before any caller writes the
// record, so a failed pull leaves the previous content, sidecar, and record set
// untouched.
//
// Content goes first and the sidecar second: if the sidecar write fails, the new
// content stays under the old provenance, which the load path reports as a
// divergence, while the reverse order would leave a sidecar naming content that
// is not there and nothing to compare. A failed write of either leaves the
// record untouched, so the next pull repairs it.
func pullRecord(name string, rec config.RemoteRecord, opts PullOptions) (pullResult, error) {
	fetch := opts.Fetch
	if fetch == nil {
		fetch = remote.Fetch
	}
	url, ref, err := remote.ResolveRecord(rec)
	if err != nil {
		return pullResult{}, fmt.Errorf("remote %q: %w", name, err)
	}
	content, commit, err := fetch(url, ref)
	if err != nil {
		return pullResult{}, fmt.Errorf("remote %q: %w", name, err)
	}
	warnings, err := remote.Gate(content, remote.GateOpts{
		Record:   name,
		NoPrefix: rec.NoPrefix,
		Merged:   gateView(opts.Merged, name),
	})
	if err != nil {
		return pullResult{}, err
	}
	if err := config.WriteRemoteContent(name, content); err != nil {
		return pullResult{}, err
	}
	if err := config.WriteRemoteState(name, config.RemoteState{
		URL:      url,
		Ref:      ref,
		Commit:   commit,
		PulledAt: time.Now().UTC(),
	}); err != nil {
		return pullResult{}, err
	}
	return pullResult{
		name:     name,
		source:   sourceLabel(rec, ref),
		profiles: projectedNames(content, name, rec.NoPrefix),
		commit:   commit,
		warnings: warnings,
	}, nil
}

// gateView is the merged view the gate judges fetched content against: every
// layer and every other remote's projection, minus this record's own projection.
// The projection under the pulled prefix is what the fetch is about to replace,
// so keeping it would make a repeat no-prefix pull report its own profiles as
// collisions and would classify an own-prefix extends target as bound to content
// that no longer exists.
func gateView(merged config.Config, name string) config.Config {
	own := config.RemoteLayer(name)
	out := merged
	out.Profiles = make(map[string]config.Profile, len(merged.Profiles))
	out.ProfileLayer = make(map[string]string, len(merged.ProfileLayer))
	out.ExtendsOrigins = make(map[string]map[string]string, len(merged.ExtendsOrigins))
	for profile, p := range merged.Profiles {
		if merged.ProfileLayer[profile] == own {
			continue
		}
		out.Profiles[profile] = p
		if layer, ok := merged.ProfileLayer[profile]; ok {
			out.ProfileLayer[profile] = layer
		}
		if origins, ok := merged.ExtendsOrigins[profile]; ok {
			out.ExtendsOrigins[profile] = origins
		}
	}
	return out
}

// projectedNames is what the fetched content will land as: the profile names the
// merged view gains once the record's pull mode is applied. The gate has already
// accepted the content, so a parse failure here can only mean the gate and this
// report disagree, which is reported as no names rather than as a failed pull.
func projectedNames(content []byte, name string, noPrefix bool) []string {
	var cfg config.Config
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil
	}
	return config.Project(cfg, name, noPrefix).ProfileNames()
}

// recordSource names a record's source in a summary or warning: the spelling the
// record holds, with the ref it resolves to when the source carries one.
func recordSource(rec config.RemoteRecord) string {
	_, ref, err := remote.ResolveRecord(rec)
	if err != nil {
		ref = rec.Ref
	}
	return sourceLabel(rec, ref)
}

// sourceLabel appends the ref to the record's spelling. A github ref is part of
// the resolved URL, but the spelling is what the user typed, so the ref is
// restated rather than left for the reader to infer.
func sourceLabel(rec config.RemoteRecord, ref string) string {
	if ref == "" {
		return rec.URL
	}
	return rec.URL + " at ref " + ref
}
