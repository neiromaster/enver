package config

import (
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

func findIndex(mapping *yaml.Node, key string) int {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Tag == "!!str" && mapping.Content[i].Value == key {
			return i + 1
		}
	}
	return -1
}

func ensureMappingEntry(mapping *yaml.Node, key string) *yaml.Node {
	if idx := findIndex(mapping, key); idx >= 0 {
		v := mapping.Content[idx]
		if v.Kind == yaml.MappingNode {
			return v
		}
		fresh := &yaml.Node{Kind: yaml.MappingNode}
		mapping.Content[idx] = fresh
		return fresh
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		&yaml.Node{Kind: yaml.MappingNode},
	)
	return mapping.Content[len(mapping.Content)-1]
}

func setScalar(mapping *yaml.Node, key, val string) {
	if idx := findIndex(mapping, key); idx >= 0 {
		mapping.Content[idx].Kind = yaml.ScalarNode
		mapping.Content[idx].Value = val
		mapping.Content[idx].Tag = "!!str"
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: val, Tag: "!!str"},
	)
}

// UpsertProfile merges profile `name` into the config file at path, preserving
// existing structure and comments via the YAML node tree. Creates the file (and
// parent dirs) if absent.
//
// Env keys merge additively (new values override existing same keys). setDefault
// updates the top-level `default` key. p.Comments[k] (non-empty) sets/overrides
// the comment above that env entry; empty/missing leaves any existing comment
// untouched.
//
// forceExtends governs how extends is written. When false, extends is set only
// when p.Extends is non-empty, so an existing extends is never cleared (import
// preserve contract). When true, extends is set to exactly p.Extends: empty
// removes the key, clearing it (mirrors WriteProfile), so callers like add and
// duplicate honor an explicit (none) choice.
func UpsertProfile(path, name string, p Profile, setDefault, forceExtends bool) error {
	if err := checkProfileEnvNames(name, p); err != nil {
		return err
	}
	root, err := loadOrInitRoot(path)
	if err != nil {
		return err
	}
	body := root.Content[0]

	prof := ensureMappingEntry(ensureMappingEntry(body, "profiles"), name)

	switch {
	case forceExtends:
		writeExtendsNode(prof, p.Extends)
	case len(p.Extends) > 0:
		writeExtendsNode(prof, p.Extends)
	}
	// unset is additive like extends: written when non-empty, never cleared by
	// an upsert, so duplicate keeps a hand-authored unset list.
	if len(p.Unset) > 0 {
		writeUnsetNode(prof, p.Unset)
	}
	if len(p.Env) > 0 {
		env := ensureMappingEntry(prof, "env")
		keys := make([]string, 0, len(p.Env))
		for k := range p.Env {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			setScalar(env, k, p.Env[k])
			if c := p.Comments[k]; c != "" {
				// idx is the value node's position; idx-1 is the key node.
				// Attaching HeadComment to the key node renders the comment on
				// the line above the KEY: entry.
				if idx := findIndex(env, k); idx >= 0 {
					env.Content[idx-1].HeadComment = c
				}
			}
		}
	}

	if setDefault {
		setScalar(body, "default", name)
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, out, 0o644)
}
