package config

import (
	"os"
	"path/filepath"
	"sort"

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
// Env keys merge additively (new values override existing same keys). Extends is
// set only when non-empty. setDefault updates the top-level `default` key.
func UpsertProfile(path, name string, p Profile, setDefault bool, comments map[string]string) error {
	var root yaml.Node
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(data) == 0 {
			root = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
		} else if err := yaml.Unmarshal(data, &root); err != nil {
			return err
		}
	case os.IsNotExist(err):
		root = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	default:
		return err
	}

	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	body := root.Content[0]

	prof := ensureMappingEntry(ensureMappingEntry(body, "profiles"), name)

	if p.Extends != "" {
		setScalar(prof, "extends", p.Extends)
	}
	if len(p.Env) > 0 {
		env := ensureMappingEntry(prof, "env")
		keys := make([]string, 0, len(p.Env))
		for k := range p.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			setScalar(env, k, p.Env[k])
			if c := comments[k]; c != "" {
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

	out, err := yaml.Marshal(&root)
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
