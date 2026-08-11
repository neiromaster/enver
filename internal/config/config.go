// Package config loads, layers and resolves enver profiles.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile is one named environment profile.
type Profile struct {
	Extends Extends           `yaml:"extends"`
	Env     map[string]string `yaml:"env"`
}

// Config is the merged top-level document.
type Config struct {
	Default  string             `yaml:"default"`
	Profiles map[string]Profile `yaml:"profiles"`
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
	return c, nil
}

// findLocal walks from cwd up to (but not including) $HOME, collecting
// .enver.yaml files. Returns them ordered home-side-first so that merging in
// order makes the nearest (cwd) override win.
func findLocal() []string {
	home := os.Getenv("HOME")
	cwd, err := os.Getwd()
	if err != nil || home == "" || !strings.HasPrefix(cwd+string(filepath.Separator), home+string(filepath.Separator)) {
		// Only layer when cwd is under $HOME.
		return nil
	}
	var paths []string
	for dir := cwd; dir != home && dir != "/" && filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
		p := filepath.Join(dir, ".enver.yaml")
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	// paths is nearest-first; reverse so cwd is applied last.
	for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
		paths[i], paths[j] = paths[j], paths[i]
	}
	return paths
}

// Merge folds override into base. Override wins for `default` and for per-key
// env values; profiles union; `extends` is taken from override when set.
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
		if len(p.Extends) > 0 {
			bp.Extends = p.Extends
		}
		if bp.Env == nil {
			bp.Env = map[string]string{}
		}
		for k, v := range p.Env {
			bp.Env[k] = v
		}
		out.Profiles[name] = bp
	}
	return out
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

// ResolveProfile resolves name's env by walking its extends graph: each parent
// is resolved transitively and merged left-to-right (a later parent overrides
// an earlier one on a shared key), then the profile's own env is applied last
// (child wins). The returned chain is a self-first DFS pre-order (parents in
// listed order, transitive, deduped) for display and comment provenance; for a
// single parent it is identical to the legacy linear chain. A cycle, including
// one spanning multiple parents, is reported as an error.
func (c Config) ResolveProfile(name string) (env map[string]string, chain []string, err error) {
	env, err = c.resolveEnv(name, map[string]bool{})
	if err != nil {
		return nil, nil, err
	}
	return env, c.chainOf(name, map[string]bool{}), nil
}

// resolveEnv returns the fully merged env for name: each parent is resolved
// transitively and merged left-to-right, then name's own env is applied last
// (child wins). The visiting set tracks the active path so a cycle (self,
// mutual, or across multiple parents) is detected; a name is removed from
// it on the way back up so a diamond is not mistaken for a cycle.
func (c Config) resolveEnv(name string, visiting map[string]bool) (map[string]string, error) {
	if visiting[name] {
		return nil, fmt.Errorf("extends cycle at %q", name)
	}
	p, ok := c.Profiles[name]
	if !ok {
		return nil, fmt.Errorf("profile %q not found", name)
	}
	visiting[name] = true
	env := map[string]string{}
	for _, parent := range p.Extends {
		pe, err := c.resolveEnv(parent, visiting)
		if err != nil {
			return nil, err
		}
		for k, v := range pe {
			env[k] = v
		}
	}
	delete(visiting, name)
	for k, v := range p.Env {
		env[k] = v
	}
	return env, nil
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

// ResolveComments walks name's extends chain and returns, for each resolved env
// key, the comment from the nearest profile in the chain that defines the key
// with a comment (child over parent). Keys with no commented definition are
// absent. Comments are read from the YAML node tree at path (the HeadComment
// above each key), so they survive encryption. A read error returns (nil, err).
func (c Config) ResolveComments(path, name string) (map[string]string, error) {
	_, chain, err := c.ResolveProfile(name)
	if err != nil {
		return nil, err
	}
	root, err := loadNode(path)
	if err != nil {
		return nil, err
	}
	comments := map[string]string{}
	pm := profilesMapping(root.Content[0])
	if pm == nil {
		return comments, nil
	}
	// chain is name→…→root; walk root→child so a nearer definer overwrites.
	for i := len(chain) - 1; i >= 0; i-- {
		idx := findIndex(pm, chain[i])
		if idx < 0 {
			continue
		}
		env := envMapping(pm.Content[idx])
		if env == nil {
			continue
		}
		for j := 0; j+1 < len(env.Content); j += 2 {
			keyNode := env.Content[j]
			c := keyNode.HeadComment
			if c == "" {
				continue
			}
			if len(c) >= 2 && c[0] == '#' && c[1] == ' ' {
				c = c[2:]
			}
			comments[keyNode.Value] = c
		}
	}
	return comments, nil
}

// ResolveCommentsMerged resolves the comments for profile name's env across the
// global config file and any local .enver.yaml layers: a comment from a nearer
// file overrides one from a farther file for the same key, and a key whose
// nearer definition carries no comment keeps the farther comment. It loads a
// fresh merged config, so it reflects the on-disk state. Keys with no commented
// definition are absent.
func ResolveCommentsMerged(globalOverride string, useLocal bool, name string) (map[string]string, error) {
	cfg, err := LoadMerged(globalOverride, useLocal)
	if err != nil {
		return nil, err
	}
	return cfg.ResolveCommentsAcross(globalOverride, useLocal, name)
}

// ResolveCommentsAcross is the merged-layer counterpart of ResolveComments: it
// resolves comments for name across the global file and any local .enver.yaml
// layers, but walks the receiver's resolved chain rather than reloading. This
// lets an in-progress edit (whose chain reflects uncommitted extends changes)
// share dotenv's merged comment provenance.
func (c Config) ResolveCommentsAcross(globalOverride string, useLocal bool, name string) (map[string]string, error) {
	_, chain, err := c.ResolveProfile(name)
	if err != nil {
		return nil, err
	}
	files := []string{GlobalPath(globalOverride)}
	if useLocal {
		files = append(files, findLocal()...) // home-side first, cwd last
	}
	return commentsFromChain(chain, files), nil
}

// commentsFromChain collects, for each env key, the comment on its nearest
// commented definer: files are walked outer-to-inner and the chain root-to-
// child within each file, so a nearer definition overwrites a farther one. A
// key with no commented definition is absent; a missing file contributes
// nothing.
func commentsFromChain(chain, files []string) map[string]string {
	comments := map[string]string{}
	for _, f := range files {
		root, err := loadNode(f)
		if err != nil {
			continue // missing/non-mapping file contributes nothing
		}
		body := root.Content[0]
		pm := profilesMapping(body)
		if pm == nil {
			continue
		}
		for i := len(chain) - 1; i >= 0; i-- { // root -> child within this file
			idx := findIndex(pm, chain[i])
			if idx < 0 {
				continue
			}
			env := envMapping(pm.Content[idx])
			if env == nil {
				continue
			}
			for j := 0; j+1 < len(env.Content); j += 2 {
				keyNode := env.Content[j]
				c := keyNode.HeadComment
				if c == "" {
					continue
				}
				if len(c) >= 2 && c[0] == '#' && c[1] == ' ' {
					c = c[2:]
				}
				comments[keyNode.Value] = c
			}
		}
	}
	return comments
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

// MaskValue redacts secret-looking values for display.
func MaskValue(k, v string) string {
	if secretRe.MatchString(k) && len(v) > 6 {
		return v[:4] + "…" + fmt.Sprintf("(len=%d)", len(v))
	}
	return v
}
