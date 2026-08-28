package config

import (
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// loadOrInitRoot reads the YAML document at path into a node tree, synthesising an
// empty mapping document when the file is missing or empty. It is the shared
// entry point for every node-tree write.
func loadOrInitRoot(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if len(data) == 0 {
			return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}, nil
		}
		var root yaml.Node
		if err := yaml.Unmarshal(data, &root); err != nil {
			return nil, err
		}
		if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
			root.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
		}
		return &root, nil
	case os.IsNotExist(err):
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}, nil
	default:
		return nil, err
	}
}

// removeKey deletes the key/value pair named key from a mapping node.
func removeKey(mapping *yaml.Node, key string) {
	idx := findIndex(mapping, key)
	if idx < 0 {
		return
	}
	mapping.Content = append(mapping.Content[:idx-1], mapping.Content[idx+1:]...)
}

// WriteProfile replaces a profile's own env wholesale (keys absent from p.Env are
// deleted), sets extends when non-empty or clears it when empty, writes unset
// with the same wholesale rule (an empty Unsets clears the field), and sets or
// clears the top-level default. p.Comments[k] (non-empty) renders above that
// entry; a key with no comment is written with no comment. Creates the file
// (and parent dirs) if absent.
func WriteProfile(path, name string, p Profile, setDefault, clearDefault bool) error {
	if err := checkProfileEnvNames(name, p); err != nil {
		return err
	}
	root, err := loadOrInitRoot(path)
	if err != nil {
		return err
	}
	body := root.Content[0]

	prof := ensureMappingEntry(ensureMappingEntry(body, "profiles"), name)

	writeExtendsNode(prof, p.Extends)
	writeUnsetNode(prof, p.Unset)

	switch {
	case len(p.Env) == 0:
		removeKey(prof, "env")
	default:
		env := ensureMappingEntry(prof, "env")
		env.Content = nil
		keys := make([]string, 0, len(p.Env))
		for k := range p.Env {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: k, Tag: "!!str"}
			valNode := &yaml.Node{Kind: yaml.ScalarNode, Value: p.Env[k], Tag: "!!str"}
			if c := p.Comments[k]; c != "" {
				keyNode.HeadComment = c
			}
			env.Content = append(env.Content, keyNode, valNode)
		}
	}

	if setDefault {
		setScalar(body, "default", name)
	}
	if clearDefault {
		removeKey(body, "default")
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return writePath(path, out)
}

// writePath marshals bytes to path, creating parent dirs (mode 0o755).
func writePath(path string, out []byte) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, out, 0o644)
}

// DeleteProfile removes the named profile from the file. Missing file or profile
// is a no-op (no error). If the removed profile was the file's default, the
// default key is cleared too — a dangling default is never valid, and a config
// without a default is a legitimate state.
func DeleteProfile(path, name string) error {
	root, err := loadNode(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	body := root.Content[0]
	pm := profilesMapping(body)
	if pm == nil {
		return nil
	}
	removeKey(pm, name)
	if dv := findIndex(body, "default"); dv >= 0 && body.Content[dv].Value == name {
		removeKey(body, "default")
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return writePath(path, out)
}
