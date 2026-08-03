package main

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
	Extends string            `yaml:"extends"`
	Env     map[string]string `yaml:"env"`
}

// Config is the merged top-level document.
type Config struct {
	Default  string             `yaml:"default"`
	Profiles map[string]Profile `yaml:"profiles"`
}

// globalConfigPath resolves the user-level config location:
// $XDG_CONFIG_HOME/enver/config.yaml, falling back to ~/.config/enver/config.yaml.
func globalConfigPath(override string) string {
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

// loadConfig reads a single YAML file. Missing file → empty Config, no error.
func loadConfig(path string) (Config, error) {
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

// findLocalConfigs walks from cwd up to (but not including) $HOME, collecting
// .enver.yaml files. Returns them ordered home-side-first so that merging in
// order makes the nearest (cwd) override win.
func findLocalConfigs() []string {
	home := os.Getenv("HOME")
	cwd, err := os.Getwd()
	if err != nil || home == "" || !strings.HasPrefix(cwd+string(filepath.Separator), home+string(filepath.Separator)) {
		// Only layer when cwd is under $HOME.
		return nil
	}
	var paths []string
	dir := cwd
	for {
		if dir == home || dir == "/" || filepath.Dir(dir) == dir {
			break
		}
		p := filepath.Join(dir, ".enver.yaml")
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
		dir = filepath.Dir(dir)
	}
	// paths is nearest-first; reverse so cwd is applied last.
	for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
		paths[i], paths[j] = paths[j], paths[i]
	}
	return paths
}

// mergeConfig folds override into base. Override wins for `default` and for
// per-key env values; profiles union; `extends` is taken from override when set.
func mergeConfig(base, override Config) Config {
	out := base
	if override.Default != "" {
		out.Default = override.Default
	}
	if out.Profiles == nil {
		out.Profiles = map[string]Profile{}
	}
	for name, p := range override.Profiles {
		bp := out.Profiles[name]
		if p.Extends != "" {
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

// loadMerged loads the global config then applies local .enver.yaml layers.
func loadMerged(globalOverride string, useLocal bool) (Config, error) {
	cfg, err := loadConfig(globalConfigPath(globalOverride))
	if err != nil {
		return cfg, err
	}
	if !useLocal {
		return cfg, nil
	}
	for _, p := range findLocalConfigs() {
		overlay, err := loadConfig(p)
		if err != nil {
			return cfg, err
		}
		cfg = mergeConfig(cfg, overlay)
	}
	return cfg, nil
}

// resolveProfile walks the `extends` chain (root applied first, child wins)
// and returns the flattened env map plus the chain (name → … → root).
func (c Config) resolveProfile(name string) (env map[string]string, chain []string, err error) {
	chain = []string{}
	cur := name
	seen := map[string]bool{}
	for {
		if seen[cur] {
			return nil, chain, fmt.Errorf("extends cycle at %q", cur)
		}
		seen[cur] = true
		p, ok := c.Profiles[cur]
		if !ok {
			return nil, chain, fmt.Errorf("profile %q not found", cur)
		}
		chain = append(chain, cur)
		if p.Extends == "" {
			break
		}
		cur = p.Extends
	}
	env = map[string]string{}
	for i := len(chain) - 1; i >= 0; i-- {
		for k, v := range c.Profiles[chain[i]].Env {
			env[k] = v
		}
	}
	return env, chain, nil
}

// profileNames returns sorted profile names.
func (c Config) profileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var secretRe = regexp.MustCompile(`(?i)(key|token|secret|password|passwd|auth|credential)`)

// maskValue redacts secret-looking values for display.
func maskValue(k, v string) string {
	if secretRe.MatchString(k) && len(v) > 6 {
		return v[:4] + "…" + fmt.Sprintf("(len=%d)", len(v))
	}
	return v
}