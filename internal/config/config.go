// Package config loads, layers and resolves enver profiles.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"

	"gopkg.in/yaml.v3"
)

// Profile is one named environment profile.
type Profile struct {
	Extends  Extends           `yaml:"extends"`
	Unset    Unsets            `yaml:"unset"` // env keys enver must not set in the resolved env
	Env      map[string]string `yaml:"env"`
	Comments map[string]string `yaml:"-"` // env key → comment; filled at decode
}

// UnmarshalYAML decodes a profile and, in the same pass, lifts the HeadComment
// of each env key into Comments — the single place that knows where comments
// live in the YAML representation.
func (p *Profile) UnmarshalYAML(value *yaml.Node) error {
	type raw Profile
	var r raw
	if err := value.Decode(&r); err != nil {
		return err
	}
	*p = Profile(r)
	env := envMapping(value)
	if env == nil {
		return nil
	}
	for i := 0; i+1 < len(env.Content); i += 2 {
		keyNode := env.Content[i]
		if c := keyNode.HeadComment; c != "" {
			if p.Comments == nil {
				p.Comments = map[string]string{}
			}
			p.Comments[keyNode.Value] = stripCommentPrefix(c)
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

// Config is the merged top-level document. Origins records, per profile and env
// key, which layer ("global" or "local") provided the winning value; it is
// filled by Merge and never serialized.
type Config struct {
	Default  string                       `yaml:"default"`
	Profiles map[string]Profile           `yaml:"profiles"`
	Origins  map[string]map[string]string `yaml:"-"` // profile → env key → layer
	// UnsetOrigins records, per profile and unset entry, which layer
	// contributed the entry; like Origins it is filled by Merge and never
	// serialized. Attribution is by exact spelling: Merge records the override
	// layer's entries under their own spelling.
	UnsetOrigins map[string]map[string]string `yaml:"-"`
}

// GlobalPath resolves the user-level config location:
// $XDG_CONFIG_HOME/enver/config.yaml, falling back to ~/.config/enver/config.yaml.
// A non-empty override is returned as-is.
func GlobalPath(override string) string {
	if override != "" {
		return override
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "enver", "config.yaml")
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "/"
	}
	return filepath.Join(home, ".config", "enver", "config.yaml")
}

// load reads a single YAML file. A missing file yields an empty Config, no error.
func load(path string) (Config, error) {
	var c Config
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := checkEnvNames(c); err != nil {
		return c, fmt.Errorf("parse %s: %w", path, err)
	}
	return c, nil
}

// LoadFile parses one YAML config file; a missing file yields an empty Config.
func LoadFile(path string) (Config, error) {
	return load(path)
}

// checkEnvNames rejects invalid env key names — hand-authored in YAML — for
// env keys and unset entries alike. Names reach eval'd export lines and child
// environments unquoted, so a name with shell metacharacters is code
// execution, not a typo to carry silently.
func checkEnvNames(c Config) error {
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
	for _, k := range sortedEnvKeys(p.Env) {
		if !ValidEnvKey(k) {
			return fmt.Errorf("profile %q defines invalid env key name %q (want [A-Za-z_][A-Za-z0-9_]*)", name, k)
		}
	}
	for _, u := range p.Unset {
		if !ValidEnvKey(u) {
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

// Merge folds override into base: override wins for default and per-key env;
// extends concatenates as [base…, override…] deduped, so local mixins compose
// with rather than replace global ones. Unsets are layer-scoped, not unioned:
// folding a shared profile consumes the inherited copy's unsets against the
// entries gathered above it (env and comments), and the overriding copy may
// add keys back — closest mention across layers wins. The merged profile
// therefore carries only the overriding layer's unset list, and UnsetOrigins
// attributes exactly those entries. Merge folds override into base's maps in
// place; the returned config shares state with base, which must not be used
// as the pre-merge view afterwards.
func Merge(base, override Config) Config {
	out := base
	if override.Default != "" {
		out.Default = override.Default
	}
	if out.Profiles == nil {
		out.Profiles = map[string]Profile{}
	}
	for name, p := range override.Profiles {
		bp := out.Profiles[name]
		// Layer-scoped unsets: this inherited copy applies its own list at
		// its merge point — stripping its entries before the override adds
		// anything — so an override refilling a key beats the inherited
		// unset, and the inherited list never rides forward.
		for _, u := range bp.Unset {
			deleteEnvKey(bp.Env, u)
			deleteEnvKey(bp.Comments, u)
		}
		bp.Extends = mergeExtends(bp.Extends, p.Extends)
		bp.Unset = appendUniq(slices.Clone(p.Unset))
		for _, u := range p.Unset {
			if out.UnsetOrigins == nil {
				out.UnsetOrigins = map[string]map[string]string{}
			}
			m := out.UnsetOrigins[name]
			if m == nil {
				m = map[string]string{}
				out.UnsetOrigins[name] = m
			}
			m[u] = LayerLocal
		}
		if bp.Env == nil {
			bp.Env = map[string]string{}
		}
		ownKeys := sortedEnvKeys(p.Env)
		for _, k := range ownKeys {
			SetEnvKey(bp.Env, k, p.Env[k])
			if out.Origins == nil {
				out.Origins = map[string]map[string]string{}
			}
			m := out.Origins[name]
			if m == nil {
				m = map[string]string{}
				out.Origins[name] = m
			}
			setEnvKeyed(m, k, LayerLocal)
		}
		for _, k := range ownKeys {
			if c := p.Comments[k]; c != "" {
				if bp.Comments == nil {
					bp.Comments = map[string]string{}
				}
				setEnvKeyed(bp.Comments, k, c)
			}
		}
		out.Profiles[name] = bp
	}
	return out
}

// mergeUniq concatenates base and add, deduplicated by EnvKeyEqual.
func mergeUniq(base, add []string) []string {
	return appendUniq(slices.Clone(base), add...)
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

// LoadMerged loads the global config then applies local .enver.yaml layers.
func LoadMerged(globalOverride string, useLocal bool) (Config, error) {
	cfg, err := load(GlobalPath(globalOverride))
	if err != nil {
		return cfg, err
	}
	if !useLocal {
		return cfg, nil
	}
	for _, p := range findLocal() {
		overlay, err := load(p)
		if err != nil {
			return cfg, err
		}
		cfg = Merge(cfg, overlay)
	}
	return cfg, nil
}

// Source identifies where a resolved env value came from: the profile that last
// set it along the chain, and the layer ("global" or "local") whose copy of
// that profile carried the winning value. A key overridden by a closer parent
// or child reports the override, matching value provenance.
type Source struct {
	Profile string `json:"profile"`
	Layer   string `json:"layer"`
}

// Resolved is the outcome of resolving a profile: the merged env, the merged
// comments (nearest commented definer wins, matching value provenance), the
// per-key source provenance, and the self-first lineage chain.
type Resolved struct {
	Env      map[string]string
	Comments map[string]string
	Sources  map[string]Source
	Chain    []string
}

// ResolveProfile resolves name: each parent is resolved transitively and
// merged left-to-right (a later parent overrides an earlier one on a shared
// key), then the profile's own env is applied last (child wins). Comments
// follow the same fold — a definer's comment applies only when it carries
// one, so a nearer uncommented redefinition keeps the farther comment. The
// returned chain is a self-first DFS pre-order for display; a cycle,
// including one spanning multiple parents, is reported as an error. Unset
// keys are dropped from the env (and comments) after the profile's own fold,
// so the closest mention of a key wins: a profile's own unset strips a
// parent's definition, and a closer redefinition overrides an ancestor's
// unset.
func (c Config) ResolveProfile(name string) (Resolved, error) {
	f, err := c.resolveEnv(name, map[string]bool{})
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{
		Env:      f.env,
		Comments: f.comments,
		Sources:  f.sources,
		Chain:    c.chainOf(name, map[string]bool{}),
	}, nil
}

// envFold is one resolution step's merged outcome.
type envFold struct {
	env      map[string]string
	comments map[string]string
	sources  map[string]Source
}

// resolveEnv returns the fully merged env, comments, and sources for name:
// each parent is resolved transitively and merged left-to-right, then name's
// own entries are applied last (child wins; comments only when the definer
// carries one). Name's own unsets are applied after its own env, so the
// closest mention of a key wins: a profile's own unset strips a parent's
// definition, and a closer redefinition overrides an ancestor's unset. The
// visiting set tracks the active path so a cycle (self, mutual, or across
// multiple parents) is detected; a name is removed from it on the way back
// up so a diamond is not mistaken for a cycle.
func (c Config) resolveEnv(name string, visiting map[string]bool) (envFold, error) {
	if visiting[name] {
		return envFold{}, fmt.Errorf("extends cycle at %q", name)
	}
	p, ok := c.Profiles[name]
	if !ok {
		return envFold{}, fmt.Errorf("profile %q not found", name)
	}
	visiting[name] = true
	var out envFold
	out.env = map[string]string{}
	out.comments = map[string]string{}
	out.sources = map[string]Source{}
	for _, parent := range p.Extends {
		pf, err := c.resolveEnv(parent, visiting)
		if err != nil {
			return envFold{}, err
		}
		for k, v := range pf.env {
			setEnvKeyed(out.env, k, v)
		}
		for k, v := range pf.comments {
			setEnvKeyed(out.comments, k, v)
		}
		for k, v := range pf.sources {
			setEnvKeyed(out.sources, k, v)
		}
	}
	delete(visiting, name)
	// Own entries apply last (child wins). Sorted so a hand-authored
	// case-variant pair (PATH and path in one env block) resolves
	// deterministically on Windows, where the later spelling wins; POSIX
	// keeps both keys, as authored.
	for _, k := range sortedEnvKeys(p.Env) {
		setEnvKeyed(out.env, k, p.Env[k])
		setEnvKeyed(out.sources, k, Source{Profile: name, Layer: c.layerOf(name, k)})
	}
	for _, k := range sortedEnvKeys(p.Comments) {
		if cc := p.Comments[k]; cc != "" {
			setEnvKeyed(out.comments, k, cc)
		}
	}
	// Own unsets apply last, stripping only this profile's own fence: a key
	// this profile unsets is removed even if a parent defined it (closest
	// mention wins), while a parent's unset does not survive a closer
	// redefinition here.
	for _, u := range p.Unset {
		deleteEnvKey(out.env, u)
		deleteEnvKey(out.comments, u)
		deleteEnvKey(out.sources, u)
	}
	return out, nil
}

// appendUniq appends entries not already present by EnvKeyEqual semantics —
// on Windows a case-variant counts as present, keeping one entry per real
// variable.
func appendUniq(dst []string, add ...string) []string {
	for _, x := range add {
		if !slices.ContainsFunc(dst, func(d string) bool { return EnvKeyEqual(d, x) }) {
			dst = append(dst, x)
		}
	}
	return dst
}

// originLookup reads m[key] by EnvKeyEqual semantics, so on Windows a
// case-variant spelling still finds the layer Merge recorded under the
// override's own spelling.
func originLookup(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	if runtime.GOOS == "windows" {
		for k, v := range m {
			if EnvKeyEqual(k, key) {
				return v
			}
		}
	}
	return ""
}

// layerOf reports which layer ("global" or "local") defined key in profile's
// own env. Merge records local for keys the local layer overrode; everything
// else is global by default.
func (c Config) layerOf(profile, key string) string {
	if l := originLookup(c.Origins[profile], key); l != "" {
		return l
	}
	return LayerGlobal
}

// unsetLayer reports which layer contributed unset entry u to profile's merged
// unset list; everything the merge did not record is global by default.
// Merged-layer Validate suppresses a same-file global contradiction when both
// layers unset a key the global layer defines (UnsetOrigins attributes local,
// env origin is global). enver validate still surfaces it only because
// validatecmd.go runs an extra ValidateGlobal pass.
func (c Config) unsetLayer(profile, key string) string {
	if l := originLookup(c.UnsetOrigins[profile], key); l != "" {
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
	sort.Strings(names)
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
	sort.Strings(out)
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

// IsSensitive reports whether a key/value pair is a secret: a secret-looking key
// name, or a value that carries credentials in a URL.
func IsSensitive(k, v string) bool {
	return secretRe.MatchString(k) || urlCredRe.MatchString(v)
}

// MaskValue redacts secret-looking values for display.
func MaskValue(k, v string) string {
	if IsSensitive(k, v) && len(v) > 6 {
		return v[:4] + "…" + fmt.Sprintf("(len=%d)", len(v))
	}
	return v
}
