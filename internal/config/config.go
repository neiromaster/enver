// Package config loads, layers and resolves enver profiles.
package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/neiromaster/enver/internal/envname"
	"github.com/neiromaster/enver/internal/xdg"
	"gopkg.in/yaml.v3"
)

// ErrExtendsCycle marks an extends chain that loops back on itself; callers
// detect it with errors.Is to tell a cycle apart from a dangling parent.
var ErrExtendsCycle = errors.New("extends cycle")

// Profile is one named environment profile.
type Profile struct {
	Extends Extends `yaml:"extends"`
	Unset   Unsets  `yaml:"unset"` // env keys enver must not set in the resolved env
	// Carried holds earlier-layer fences whose targets arrive only through
	// extends ancestors, so Merge could not consume them in place. Each fence
	// records the layer whose copy declared it, so resolveEnv can attribute
	// the tombstone it reports. resolveEnv applies the fences between the
	// inherited fold and the profile's own entries, going silent when the
	// winning mention already belongs to the later layer. Never serialized;
	// always empty for load-built profiles.
	Carried  []CarriedFence    `yaml:"-"`
	Env      map[string]string `yaml:"env"`
	Comments map[string]string `yaml:"-"` // env key → comment; filled at decode
	// Marked holds the env keys written with a secret mark, by runtime name;
	// nil when the decoded profile carries none. Filled at decode.
	Marked map[string]bool `yaml:"-"`
}

// UnmarshalYAML decodes a profile and, in the same pass, rebuilds its env from
// the YAML nodes — stripping the secret mark into Marked, rejecting a malformed
// one, and keying each HeadComment by the stripped name. The plain map decode
// cannot: it leaves the mark in the keys and drops a bare `~` key silently. The
// unset list loses the mark too, so a fence spelled with it still bites.
func (p *Profile) UnmarshalYAML(value *yaml.Node) error {
	type raw Profile
	var r raw
	if err := value.Decode(&r); err != nil {
		return err
	}
	*p = Profile(r)
	var unset Unsets
	for _, u := range p.Unset {
		name, _ := envname.SplitMarked(u)
		unset = appendUniq(unset, name)
	}
	p.Unset = unset
	env := envMapping(value)
	if env == nil {
		// The decoder resolved an env the node walk cannot see; a silent drop
		// would lose the whole environment, so refuse the shape instead.
		if len(r.Env) > 0 {
			return fmt.Errorf("profile env is not a mapping enver can read")
		}
		return nil
	}
	p.Env = nil
	if err := p.applyEnvMapping(env); err != nil {
		return err
	}
	p.Marked = markedOrNil(p.Marked)
	return nil
}

// applyEnvMapping records one env mapping's entries, merge keys included: `<<`
// sources land lowest priority first, so an explicit key still overrides the
// merged copy it shares a name with, and a mark written inside an anchor is
// honored. Names collide only within the mapping that spells them out — an
// override is what a merge is for. A plain override clears a merged mark, the
// same way any later mention of a key wins.
func (p *Profile) applyEnvMapping(env *yaml.Node) error {
	sources := mergeSources(env)
	for i := len(sources) - 1; i >= 0; i-- {
		if err := p.applyEnvMapping(sources[i]); err != nil {
			return err
		}
	}
	authored := map[string]string{} // runtime name → key as written
	for i := 0; i+1 < len(env.Content); i += 2 {
		keyNode, valNode := env.Content[i], env.Content[i+1]
		if keyNode.Tag == "!!merge" {
			continue
		}
		name, marked := envname.SplitMarked(keyNode.Value)
		if name == "" && marked {
			return fmt.Errorf("env key %q carries the secret mark but no name", keyNode.Value)
		}
		if envname.IsMarked(name) || name == string(envname.SecretMark) {
			return fmt.Errorf("env key %q has a doubled secret mark; write one `~`", keyNode.Value)
		}
		if prev, ok := envname.Get(authored, name); ok {
			return fmt.Errorf("env keys %q and %q are the same variable; keep one", prev, keyNode.Value)
		}
		var v string
		if err := valNode.Decode(&v); err != nil {
			return err
		}
		envname.Set(authored, name, keyNode.Value)
		if p.Env == nil {
			p.Env = map[string]string{}
		}
		envname.Set(p.Env, name, v)
		if marked {
			if p.Marked == nil {
				p.Marked = map[string]bool{}
			}
			envname.Set(p.Marked, name, true)
		} else {
			envname.Delete(p.Marked, name)
		}
		if c := keyNode.HeadComment; c != "" {
			if p.Comments == nil {
				p.Comments = map[string]string{}
			}
			p.Comments[name] = stripCommentPrefix(c)
		}
	}
	return nil
}

// stripCommentPrefix removes the leading "# " the YAML parser keeps on the
// first line of a HeadComment. Later lines keep their prefixes; the dotenv
// formatter normalizes per line at render time.
func stripCommentPrefix(c string) string {
	if len(c) >= 2 && c[0] == '#' && c[1] == ' ' {
		return c[2:]
	}
	return c
}

// Layer names recorded in Config.Origins for per-key provenance.
const (
	LayerGlobal = "global"
	LayerLocal  = "local"
)

// remoteLayerPrefix heads the label a remote's content carries, followed by the
// record name.
const remoteLayerPrefix = "remote:"

// Layer is one config stacked with the provenance label readers know it by: the
// label Merge stamps onto its content and LayerEra ranks. A write folds the
// stack its target sits on, so a label inherited from any of those layers keeps
// its era and its mark.
type Layer struct {
	Label  string
	Config Config
}

// RemoteName returns the record name a remote layer label carries. ok is false
// for a label that names a user layer.
func RemoteName(layer string) (string, bool) {
	return strings.CutPrefix(layer, remoteLayerPrefix)
}

// IsRemoteLayer reports whether layer names content a remote supplied. Callers
// that must tell a remote's values from the user's own — a decrypt failure a
// consumer may not be able to fix, say — read it here rather than spell the
// prefix themselves.
func IsRemoteLayer(layer string) bool {
	_, ok := RemoteName(layer)
	return ok
}

// LayerEra ranks a layer label by its height in the fold: remote content is
// the lowest base, global folds over it, local highest. An unrecognized label
// ranks as global, the layer the attribution fallbacks already assume.
func LayerEra(layer string) int {
	switch {
	case IsRemoteLayer(layer):
		return 0
	case layer == LayerLocal:
		return 2
	default:
		return 1
	}
}

// Config is the merged top-level document. Origins records, per profile and env
// key, which layer ("global", "local", or "remote:<name>") provided the winning
// value; it is filled by Merge and never serialized.
type Config struct {
	Default  string                  `yaml:"default"`
	Profiles map[string]Profile      `yaml:"profiles"`
	Remotes  map[string]RemoteRecord `yaml:"remotes"` // record name → source; Merge folds by name
	// Warnings holds the load-time advisories about the remotes content on disk:
	// content fetched from a source its record no longer points at, and content
	// whose provenance no sidecar records. config never prints — the load path is
	// shared by shell completion and machine-output commands — so commands render
	// these and the machine paths drop them. Filled by LoadMerged, never
	// serialized, and dropped by Merge, which describes a different view.
	Warnings []string                     `yaml:"-"`
	Origins  map[string]map[string]string `yaml:"-"` // profile → env key → layer
	// UnsetOrigins records, per profile and unset entry, which layer
	// contributed the entry; like Origins it is filled by Merge and never
	// serialized. Attribution is by exact spelling: Merge records the override
	// layer's entries under their own spelling.
	UnsetOrigins map[string]map[string]string `yaml:"-"`
	// ProfileLayer records, per profile name, the layer whose copy declares
	// it; like Origins it is filled by Merge and never serialized. An
	// overriding copy restamps the name, a carried entry keeps its layer.
	ProfileLayer map[string]string `yaml:"-"`
	// ExtendsOrigins records, per profile and extends target, the layer whose
	// copy declared the reference; like Origins it is filled by Merge and never
	// serialized. An overriding copy restamps only the targets it names itself,
	// so a reference a remote authored stays the remote's even after a user
	// patch takes over the profile name.
	ExtendsOrigins map[string]map[string]string `yaml:"-"`
}

// GlobalPath resolves the user-level config location:
// $XDG_CONFIG_HOME/enver/config.yaml, falling back to ~/.config/enver/config.yaml.
// A non-empty override is returned as-is.
func GlobalPath(override string) string {
	if override != "" {
		return override
	}
	return filepath.Join(xdg.ConfigHome(), "enver", "config.yaml")
}

// load reads a single YAML file. A missing file yields an empty Config, no error.
func load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	return parseConfig(path, data)
}

// parseConfig decodes one config document and applies the load-time name
// validation every enver config file gets. Remote content goes through it too:
// a remote is just a config file, so invalid names a pull somehow landed are
// refused at load rather than resolved.
func parseConfig(path string, data []byte) (Config, error) {
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := CheckEnvNames(c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// LoadFile parses one YAML config file; a missing file yields an empty Config.
func LoadFile(path string) (Config, error) {
	return load(path)
}

// CheckEnvNames rejects invalid names — hand-authored in YAML — for env keys
// and unset entries, and for remote record names. Env names reach eval'd
// export lines and child environments unquoted, so a name with shell
// metacharacters is code execution. Record names become profile prefixes and
// content file names, so one outside the single-level rule is an escape from
// the namespace pull writes into. Neither is a typo to carry silently. Every
// file the load path parses gets this check; the pull gate applies it to
// fetched content too, so a file load would refuse never reaches disk.
func CheckEnvNames(c Config) error {
	for _, name := range slices.Sorted(maps.Keys(c.Remotes)) {
		if !ValidRecordName(name) {
			return fmt.Errorf("remotes defines invalid record name %q (want [A-Za-z0-9][A-Za-z0-9_-]*)", name)
		}
	}
	for _, name := range c.ProfileNames() {
		if err := checkProfileEnvNames(name, c.Profiles[name]); err != nil {
			return err
		}
	}
	return nil
}

// checkProfileEnvNames is the write-side twin of the load-time check: no
// invalid name is ever written, whatever authored the Profile struct.
func checkProfileEnvNames(name string, p Profile) error {
	for _, k := range slices.Sorted(maps.Keys(p.Env)) {
		if !envname.Valid(k) {
			return fmt.Errorf("profile %q defines invalid env key name %q (want [A-Za-z_][A-Za-z0-9_]*)", name, k)
		}
	}
	for _, u := range p.Unset {
		if !envname.Valid(u) {
			return fmt.Errorf("profile %q unsets invalid env key name %q (want [A-Za-z_][A-Za-z0-9_]*)", name, u)
		}
	}
	return nil
}

const LocalFilename = ".enver.yaml"

// LocalPath is the local layer: LocalFilename in cwd. No walk-up.
func LocalPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return LocalFilename
	}
	return filepath.Join(cwd, LocalFilename)
}

// findLocal reports the local layer as a 0-or-1-element slice for LoadMerged.
func findLocal() []string {
	p := LocalPath()
	if _, err := os.Stat(p); err != nil {
		return nil
	}
	return []string{p}
}

// Merge folds override into base under per-call layer labels, baseLabel for
// the base layer and overrideLabel for the overriding one, so a fold deeper
// than two layers stamps content with the layer that really authored it
// instead of positional defaults. The override's env keys and declared unsets
// carry overrideLabel; base entries no earlier fold attributed — fences, env
// keys, profile names — carry baseLabel, so a lower base layer keeps its own
// era rather than reading as the next fold's base. Otherwise override wins
// for default, per-key env, and remotes by name — an overriding record
// replaces the inherited one whole, ref included, while a record the override
// does not redeclare survives — except that an empty override Default never
// clears the base default: clearing is per-layer, as `enver default --clear`
// writes the owning file. Extends concatenates as [base…, override…] deduped,
// so local mixins compose with rather than replace global ones. Unsets are
// layer-scoped, not unioned: folding a shared profile applies the inherited
// copy's fence to its own env right here — the overriding copy may refill
// those keys — while a fence whose target arrives only through extends
// ancestors moves to Profile.Carried, where resolveEnv applies it era-gated
// after the inherited fold. The merged profile's declared list is the
// overriding layer's own, and UnsetOrigins attributes exactly those entries;
// profiles absent from the override shed their earlier-era declared fences the
// same way. Warnings describe the view a config was loaded as, so the fold drops
// them: the merged config's advisories are the merge caller's to compute.
// Merge never mutates its inputs: the fold writes into clones, so the returned
// config shares no state with base or override.
func Merge(base, override Config, baseLabel, overrideLabel string) Config {
	out := base
	out.Warnings = nil
	out.Profiles = cloneProfiles(base.Profiles)
	out.Origins = cloneProvenance(base.Origins)
	out.UnsetOrigins = cloneProvenance(base.UnsetOrigins)
	out.ProfileLayer = maps.Clone(base.ProfileLayer)
	out.ExtendsOrigins = cloneProvenance(base.ExtendsOrigins)
	out.Remotes = mergeRemotes(base.Remotes, override.Remotes)
	if override.Default != "" {
		out.Default = override.Default
	}
	if out.Profiles == nil {
		out.Profiles = map[string]Profile{}
	}
	for name, p := range override.Profiles {
		bp := out.Profiles[name]
		bp.Extends = mergeExtends(bp.Extends, p.Extends)
		for _, target := range p.Extends {
			out.extendsOrigin(name, target, overrideLabel)
		}
		splitUnsets(&bp, p.Unset, base.UnsetOrigins[name], baseLabel)
		for _, u := range p.Unset {
			if out.UnsetOrigins == nil {
				out.UnsetOrigins = map[string]map[string]string{}
			}
			m := out.UnsetOrigins[name]
			if m == nil {
				m = map[string]string{}
				out.UnsetOrigins[name] = m
			}
			m[u] = overrideLabel
		}
		if bp.Env == nil {
			bp.Env = map[string]string{}
		}
		if bp.Marked == nil {
			bp.Marked = map[string]bool{}
		}
		ownKeys := slices.Sorted(maps.Keys(p.Env))
		for _, k := range ownKeys {
			envname.Set(bp.Env, k, p.Env[k])
			envname.Delete(bp.Marked, k)
			if p.Marked[k] {
				envname.Set(bp.Marked, k, true)
			}
			if out.Origins == nil {
				out.Origins = map[string]map[string]string{}
			}
			m := out.Origins[name]
			if m == nil {
				m = map[string]string{}
				out.Origins[name] = m
			}
			envname.Set(m, k, overrideLabel)
		}
		for _, k := range ownKeys {
			if c := p.Comments[k]; c != "" {
				if bp.Comments == nil {
					bp.Comments = map[string]string{}
				}
				envname.Set(bp.Comments, k, c)
			}
		}
		bp.Marked = markedOrNil(bp.Marked)
		out.Profiles[name] = bp
		if out.ProfileLayer == nil {
			out.ProfileLayer = map[string]string{}
		}
		out.ProfileLayer[name] = overrideLabel
	}
	// Profiles without an overriding copy resolve exactly as before; only
	// their era-declared fences need the same consume-or-carry split.
	for name := range out.Profiles {
		if _, overlaid := override.Profiles[name]; overlaid {
			continue
		}
		bp := out.Profiles[name]
		splitUnsets(&bp, nil, base.UnsetOrigins[name], baseLabel)
		bp.Marked = markedOrNil(bp.Marked)
		out.Profiles[name] = bp
	}
	// What no earlier fold attributed is the base layer's own: stamp env keys,
	// profile names, and extends targets that carry no recorded layer with
	// baseLabel, so a lower base layer keeps its era instead of falling back to
	// global.
	for name, bp := range out.Profiles {
		m := out.Origins[name]
		for _, k := range slices.Sorted(maps.Keys(bp.Env)) {
			if _, ok := envname.Get(m, k); ok {
				continue
			}
			if out.Origins == nil {
				out.Origins = map[string]map[string]string{}
			}
			if m == nil {
				m = map[string]string{}
				out.Origins[name] = m
			}
			envname.Set(m, k, baseLabel)
		}
		if _, ok := out.ProfileLayer[name]; !ok {
			if out.ProfileLayer == nil {
				out.ProfileLayer = map[string]string{}
			}
			out.ProfileLayer[name] = baseLabel
		}
		for _, target := range bp.Extends {
			if _, ok := out.ExtendsOrigins[name][target]; ok {
				continue
			}
			out.extendsOrigin(name, target, baseLabel)
		}
	}
	return out
}

// extendsOrigin records layer as the authoring layer of one extends target,
// creating the per-profile map on first use. Attribution is by exact target
// spelling, matching the merge that dedupes the lists.
func (o *Config) extendsOrigin(profile, target, layer string) {
	if o.ExtendsOrigins == nil {
		o.ExtendsOrigins = map[string]map[string]string{}
	}
	m := o.ExtendsOrigins[profile]
	if m == nil {
		m = map[string]string{}
		o.ExtendsOrigins[profile] = m
	}
	m[target] = layer
}

// markedOrNil keeps the Marked nil contract: a set that holds no mark is nil,
// never an empty map, so "no information" stays distinguishable from "no marks".
func markedOrNil(m map[string]bool) map[string]bool {
	if len(m) == 0 {
		return nil
	}
	return m
}

// cloneProfiles copies the profiles map and every per-profile slice and map
// the fold replaces or appends to, so mutations never reach the inputs.
func cloneProfiles(in map[string]Profile) map[string]Profile {
	out := make(map[string]Profile, len(in))
	for name, p := range in {
		p.Extends = slices.Clone(p.Extends)
		p.Unset = slices.Clone(p.Unset)
		p.Carried = slices.Clone(p.Carried)
		p.Env = maps.Clone(p.Env)
		p.Comments = maps.Clone(p.Comments)
		p.Marked = maps.Clone(p.Marked)
		out[name] = p
	}
	return out
}

// cloneProvenance copies a provenance map (outer map plus each inner map) so
// mutations never reach the inputs. Nil-safe.
func cloneProvenance(in map[string]map[string]string) map[string]map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]map[string]string, len(in))
	for profile, inner := range in {
		out[profile] = maps.Clone(inner)
	}
	return out
}

// splitUnsets sheds the inherited copy's era in favor of the overriding one:
// every inherited declared fence rides forward in Carried for era-gated
// application at resolve time (so an ancestor's value cannot resurface just
// because a local file exists), stamped with the layer that declared it — the
// unset provenance of the config whose copy the fences leave, baseLabel where
// nothing is recorded — and a fence whose target sits in this copy's own env
// additionally drops that entry right at the fold — leaving it would
// resurrect the value through the own-env overlay.
func splitUnsets(bp *Profile, fresh Unsets, inheritedOrigins map[string]string, baseLabel string) {
	for _, u := range bp.Unset {
		layer := baseLabel
		if l, ok := envname.Get(inheritedOrigins, u); ok {
			layer = l
		}
		bp.Carried = carryFence(bp.Carried, CarriedFence{Key: u, Layer: layer})
		if envname.Has(bp.Env, u) {
			envname.Delete(bp.Env, u)
			envname.Delete(bp.Comments, u)
			envname.Delete(bp.Marked, u)
		}
	}
	bp.Unset = slices.Clone(fresh)
}

// carryFence records a moved unset in Carried, replacing any existing fence
// for the same key (case-variants too on Windows): a later layer's
// re-declaration supersedes the earlier one's attribution.
func carryFence(dst []CarriedFence, f CarriedFence) []CarriedFence {
	for i := range dst {
		if envname.Equal(dst[i].Key, f.Key) {
			dst[i] = f
			return dst
		}
	}
	return append(dst, f)
}

// mergeExtends concatenates base and add, deduplicated byte-exactly: profile
// names are case-sensitive on every platform, so a Windows case-insensitive
// dedup would wrongly collapse [Prod] + [prod] into one parent.
func mergeExtends(base, add Extends) Extends {
	out := slices.Clone(base)
	for _, x := range add {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return Extends(out)
}

// LoadMerged loads the global config, folds the content of every pulled remote
// in below it, then applies local .enver.yaml layers. Layer order is fixed no
// matter which layer declares a record — local above global above remote — so a
// remote never changes what a user-authored profile resolves to, and adding one
// cannot disturb a profile chain that never references remote content. The
// fold's top-level default comes from the user layers only: projection drops a
// remote's default, which is the user's decision to make.
// Config.Warnings is filled last, with the advisories the merged view raises:
// content pulled from a source the record no longer points at, content no
// sidecar records, content whose record no longer resolves, stored content that
// cannot be read, and stored content no record declares. Merge drops warnings by
// design, so nothing may assign them before the fold is done.
func LoadMerged(globalOverride string, useLocal bool) (Config, error) {
	global, err := load(GlobalPath(globalOverride))
	if err != nil {
		return global, err
	}
	var locals []Config
	if useLocal {
		for _, p := range findLocal() {
			overlay, err := load(p)
			if err != nil {
				return global, err
			}
			locals = append(locals, overlay)
		}
	}
	records := global.Remotes
	for _, overlay := range locals {
		records = mergeRemotes(records, overlay.Remotes)
	}
	base, remoteBaseLabel, skippedContent, err := remoteBase(records)
	if err != nil {
		return global, err
	}
	if remoteBaseLabel == "" {
		remoteBaseLabel = LayerGlobal
	}
	cfg := Merge(base, global, remoteBaseLabel, LayerGlobal)
	for _, overlay := range locals {
		cfg = Merge(cfg, overlay, LayerGlobal, LayerLocal)
	}
	cfg.Warnings = slices.Concat(skippedContent, orphanWarnings(records), remoteWarnings(cfg.Remotes))
	return cfg, nil
}

// Source identifies where a resolved env value came from: the profile that last
// set it along the chain, and the layer whose copy of that profile carried the
// winning value ("global", "local", or "remote:<name>"). A key overridden by a
// closer parent or child reports the override, matching value provenance.
type Source struct {
	Profile string `json:"profile"`
	Layer   string `json:"layer"`
}

// Resolved is the outcome of resolving a profile: the merged env, the merged
// comments (nearest commented definer wins, matching value provenance), the
// per-key source provenance, the secret marks whose winning entry carried one,
// the unset tombstones, and the self-first lineage chain. Encrypted and
// Undecryptable are filled in by the run layer, not by resolution: config
// recognizes ciphertext but never decrypts, so the at-rest report belongs to
// the caller that does.
type Resolved struct {
	Env      map[string]string
	Comments map[string]string
	Sources  map[string]Source
	// Marked holds the keys written with a secret mark by their winning entry;
	// nil when no key ends up marked.
	Marked map[string]bool
	// Encrypted holds the keys whose value was ciphertext at rest before
	// resolution decrypted it; nil when no value was encrypted.
	Encrypted map[string]bool
	// Undecryptable holds the keys whose ciphertext stayed unread because no key
	// source resolved, each with the reason to display; nil when every value was
	// readable.
	Undecryptable map[string]string
	// Unsets reports the keys deliberately absent from Env, each attributed to
	// the profile whose unset last removed (or declared) it. A closer
	// redefinition clears the tombstone; a tombstone never coexists with a
	// live key.
	Unsets map[string]Source
	Chain  []string
}

// ResolveProfile resolves name: each parent is resolved transitively and
// merged left-to-right (a later parent overrides an earlier one on a shared
// key), then the profile's own env is applied last (child wins). Comments
// follow the same fold — a definer's comment applies only when it carries
// one, so a nearer uncommented redefinition keeps the farther comment. Marks
// ride with values the same way: the nearest mention decides, so a closer
// unmarked definition clears an inherited mark. The returned chain is a
// self-first DFS pre-order for display; a cycle, including one spanning
// multiple parents, is reported as an error. Unset keys are dropped from the
// env (and comments) after the profile's own fold, so the closest mention of a
// key wins: a profile's own unset strips a parent's definition, and a closer
// redefinition overrides an ancestor's unset. Every key the chain deliberately
// absent — declared unsets with or without a victim, carried fences — is
// reported in Unsets with the unsetting profile and layer; a key back in play
// holds no tombstone.
func (c Config) ResolveProfile(name string) (Resolved, error) {
	f, err := c.resolveEnv(name, map[string]bool{})
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{
		Env:      f.env,
		Comments: f.comments,
		Sources:  f.sources,
		Marked:   markedOrNil(f.marked),
		Unsets:   f.unsets,
		Chain:    c.chainOf(name, map[string]bool{}),
	}, nil
}

// envFold is one resolution step's merged outcome.
type envFold struct {
	env      map[string]string
	comments map[string]string
	sources  map[string]Source
	marked   map[string]bool
	unsets   map[string]Source
}

// profileNotFound is the error a reference to a name no layer defines gets: the
// plain miss, plus the pull that would supply it when the name is a prefixed
// profile of a declared but unpulled record. Pull cannot help an undeclared
// prefix, so that case keeps the plain message.
func (c Config) profileNotFound(name string) error {
	if hint := c.pullHint(name); hint != "" {
		return fmt.Errorf("profile %q not found (%s)", name, hint)
	}
	return fmt.Errorf("profile %q not found", name)
}

// resolveEnv returns the fully merged env, comments, sources, secret marks, and
// unset tombstones for name: each parent is resolved transitively and merged
// left-to-right, then name's own entries are applied last (child wins;
// comments only when the definer carries one). Each mention moves the key's
// mark with its value, so the winning entry decides whether the key is marked.
// Name's own unsets are applied after its own env, so the closest mention of a
// key wins: a profile's own unset strips a parent's definition, and a closer
// redefinition overrides an ancestor's unset. Tombstones follow the env:
// setting a key clears its tombstone, an unset (declared or carried) records
// one attributed to the unsetting profile, and the final sweep drops any
// tombstone whose key a sibling parent's define kept alive. The visiting set
// tracks the active path so a cycle (self, mutual, or across multiple parents)
// is detected; a name is removed from it on the way back up so a diamond is not
// mistaken for a cycle.
func (c Config) resolveEnv(name string, visiting map[string]bool) (envFold, error) {
	if visiting[name] {
		return envFold{}, fmt.Errorf("%w at %q", ErrExtendsCycle, name)
	}
	p, ok := c.Profiles[name]
	if !ok {
		return envFold{}, c.profileNotFound(name)
	}
	visiting[name] = true
	var out envFold
	out.env = map[string]string{}
	out.comments = map[string]string{}
	out.sources = map[string]Source{}
	out.marked = map[string]bool{}
	out.unsets = map[string]Source{}
	for _, parent := range p.Extends {
		pf, err := c.resolveEnv(parent, visiting)
		if err != nil {
			return envFold{}, err
		}
		// The mark fold is dense like the value fold: every key this parent
		// defines takes its mark from this parent alone, so a later unmarked
		// entry clears the mark an earlier sibling put there.
		for k, v := range pf.env {
			envname.Set(out.env, k, v)
			envname.Delete(out.marked, k)
		}
		for k, v := range pf.comments {
			envname.Set(out.comments, k, v)
		}
		for k, v := range pf.sources {
			envname.Set(out.sources, k, v)
		}
		for k, v := range pf.marked {
			envname.Set(out.marked, k, v)
		}
		for k, v := range pf.unsets {
			envname.Set(out.unsets, k, v)
		}
	}
	delete(visiting, name)
	// Carried earlier-era fences act between the inherited contributions and
	// this profile's own entries: they strip what the earlier layer supplied,
	// and go silent the moment a mention from a higher layer owns the key —
	// LayerEra ranks the two labels, so a fence bites its own era and below.
	// Every unsilenced fence reports a tombstone attributed to this profile
	// at the layer whose copy declared the fence: a fence with no victim
	// alive still carries that declaration's intent.
	for _, u := range p.Carried {
		if s, ok := envname.Get(out.sources, u.Key); ok && LayerEra(s.Layer) > LayerEra(u.Layer) {
			continue
		}
		envname.Delete(out.env, u.Key)
		envname.Delete(out.comments, u.Key)
		envname.Delete(out.sources, u.Key)
		envname.Delete(out.marked, u.Key)
		envname.Set(out.unsets, u.Key, Source{Profile: name, Layer: u.Layer})
	}
	// Own entries apply last (child wins). Sorted so a hand-authored
	// case-variant pair (PATH and path in one env block) resolves
	// deterministically on Windows, where the later spelling wins; POSIX
	// keeps both keys, as authored.
	for _, k := range slices.Sorted(maps.Keys(p.Env)) {
		envname.Set(out.env, k, p.Env[k])
		envname.Delete(out.marked, k)
		if p.Marked[k] {
			envname.Set(out.marked, k, true)
		}
		envname.Set(out.sources, k, Source{Profile: name, Layer: c.layerOf(name, k)})
		envname.Delete(out.unsets, k)
	}
	for _, k := range slices.Sorted(maps.Keys(p.Comments)) {
		if cc := p.Comments[k]; cc != "" {
			envname.Set(out.comments, k, cc)
		}
	}
	// Own unsets apply last, stripping only this profile's own fence: a key
	// this profile unsets is removed even if a parent defined it (closest
	// mention wins), while a parent's unset does not survive a closer
	// redefinition here. Every declared entry reports a tombstone, with or
	// without a victim: the list is the profile's stated intent.
	for _, u := range p.Unset {
		envname.Delete(out.env, u)
		envname.Delete(out.comments, u)
		envname.Delete(out.sources, u)
		envname.Delete(out.marked, u)
		envname.Set(out.unsets, u, Source{Profile: name, Layer: c.unsetLayer(name, u)})
	}
	// A tombstone never coexists with a live key, whichever order sibling
	// parents fold in: parents arrive pre-folded, so the unsetting sibling's
	// tombstone can land after the victim is already resident from the
	// sibling that defines it. The final sweep drops stale entries in both
	// orders — it is the invariant's single enforcement point. A mark rides
	// with a live value, so a surviving tombstone takes its key's mark along.
	for k := range out.unsets {
		if envname.Has(out.env, k) {
			envname.Delete(out.unsets, k)
			continue
		}
		envname.Delete(out.marked, k)
	}
	return out, nil
}

// MarksForWrite answers the secret-mark question for a caller writing keys into
// profile from another layer, where the writers cannot see the ancestry
// themselves: the profile's own entry decides first — an unmarked own entry
// outranks an inherited mark, exactly as resolution folds it — the resolved
// chain supplies the keys the profile does not define, and a key marked nowhere
// is false. The result is non-nil and covers every requested key, so a writer
// takes it as authoritative, which is why a profile this config cannot resolve
// (cycle, dangling parent, unknown name) is no mark rather than an error.
func MarksForWrite(cfg Config, profile string, keys []string) map[string]bool {
	own := cfg.Profiles[profile]
	var chain map[string]bool
	if slices.ContainsFunc(keys, func(k string) bool { return !envname.Has(own.Env, k) }) {
		if r, err := cfg.ResolveProfile(profile); err == nil {
			chain = r.Marked
		}
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		if envname.Has(own.Env, k) {
			out[k], _ = envname.Get(own.Marked, k)
			continue
		}
		out[k], _ = envname.Get(chain, k)
	}
	return out
}

// appendUniq appends entries not already present by envname.Equal semantics —
// on Windows a case-variant counts as present, keeping one entry per real
// variable.
func appendUniq(dst []string, add ...string) []string {
	for _, x := range add {
		if !slices.ContainsFunc(dst, func(d string) bool { return envname.Equal(d, x) }) {
			dst = append(dst, x)
		}
	}
	return dst
}

// layerOf reports which layer ("global", "local", or "remote:<name>") defined
// key in profile's own env. Merge records the layer that overrode or authored
// the key; everything unrecorded is global by default.
func (c Config) layerOf(profile, key string) string {
	if l, ok := envname.Get(c.Origins[profile], key); ok {
		return l
	}
	return LayerGlobal
}

// unsetLayer reports which layer contributed unset entry u to profile's merged
// unset list; everything the merge did not record is global by default.
// Merged-layer Validate suppresses a same-file global contradiction when both
// layers unset a key the global layer defines (UnsetOrigins attributes local,
// env origin is global). enver validate still surfaces it only because the CLI
// adds an extra isolated-global pass on top of the merged one.
func (c Config) unsetLayer(profile, key string) string {
	if l, ok := envname.Get(c.UnsetOrigins[profile], key); ok {
		return l
	}
	return LayerGlobal
}

// chainOf returns name's lineage as a self-first DFS pre-order: name, then each
// parent (in listed order) and its lineage transitively, skipping names already
// emitted. The caller must ensure the graph is acyclic — ResolveProfile checks
// via resolveEnv before calling this.
func (c Config) chainOf(name string, seen map[string]bool) []string {
	if seen[name] {
		return nil
	}
	seen[name] = true
	out := []string{name}
	for _, parent := range c.Profiles[name].Extends {
		out = append(out, c.chainOf(parent, seen)...)
	}
	return out
}

// ProfileNames returns the profile names sorted alphabetically.
func (c Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// ExtendedBy returns the names of profiles that extend name, sorted.
func (c Config) ExtendedBy(name string) []string {
	var out []string
	for n, p := range c.Profiles {
		if p.Extends.Has(name) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out
}

var secretRe = regexp.MustCompile(`(?i)(key|token|secret|password|passwd|auth|credential)`)

// urlCredRe matches a value that embeds credentials in a URL authority, e.g.
// postgres://user:pass@db.internal:5432/app. Such values are secrets even under
// a generic key (DATABASE_URL, CONNECTION_STRING). The userinfo is matched
// before the @ so a password may contain `/` and a token may be the bare
// username (https://TOKEN@github.com/...). Plain URLs without a userinfo
// component (https://api.example.com) are not secrets.
var urlCredRe = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.\-]*://(?:[^/@\s:]*:[^@\s]*|[^/@\s:]+)@`)

// IsSensitive reports whether a key/value pair should be masked for display:
// a secret-looking key name, or a value that carries credentials in a URL.
func IsSensitive(k, v string) bool {
	return secretRe.MatchString(k) || urlCredRe.MatchString(v)
}

// ShouldEncrypt reports whether a key/value pair should be stored encrypted.
// It shares the secret heuristic with display masking on purpose, but is a
// separate predicate: the security-side selection may grow stricter than the
// display-side one without rewriting every mask call site. A key written with
// the secret mark is a candidate whatever its name or value.
func ShouldEncrypt(k, v string) bool {
	name, marked := envname.SplitMarked(k)
	return marked || secretRe.MatchString(name) || urlCredRe.MatchString(v)
}

// ShouldMask reports whether a resolved value must be masked in text output:
// the winning entry carries the secret mark, the value is ciphertext at rest,
// or the name/value heuristic flags it. Display masking is separate from
// ShouldEncrypt so a value encrypted without a mark, or a marked value not yet
// encrypted, still hides on screen.
func ShouldMask(k, v string, marked, encrypted bool) bool {
	return marked || encrypted || IsSensitive(k, v)
}

// MaskValue redacts a value for display. A secret shows its first four
// characters only when the value is long enough to keep the prefix no more
// than a third of it; shorter values reveal just their length.
func MaskValue(v string, secret bool) string {
	if !secret || v == "" {
		return v
	}
	if len(v) >= 12 {
		return v[:4] + "…" + fmt.Sprintf("(len=%d)", len(v))
	}
	return fmt.Sprintf("(len=%d)", len(v))
}
