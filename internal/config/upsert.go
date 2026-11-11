package config

import (
	"slices"

	"github.com/neiromaster/enver/internal/envname"
	"gopkg.in/yaml.v3"
)

// findIndex locates key in a mapping node's content. Keys match by scalar
// value across tags: a hand-authored bool-spelled key (`TRUE:`) decodes with
// a !!bool tag but the same literal text the struct reader exposes, and the
// writer must find that entry rather than append a duplicate. Merge keys
// never match.
func findIndex(mapping *yaml.Node, key string) int {
	return findIndexWhere(mapping, func(literal string) bool { return literal == key })
}

// findEnvIndex locates an env entry by the runtime name the decoder exposes,
// whatever secret mark the key was written with: a writer that appends a plain
// sibling to a marked entry would leave the file unreadable.
func findEnvIndex(mapping *yaml.Node, name string) int {
	return findIndexWhere(mapping, func(literal string) bool {
		existing, _ := envname.SplitMarked(literal)
		return existing == name
	})
}

// findIndexWhere returns the value node of the first entry whose key literal
// match accepts, or -1. Merge keys are directives, never entries to match.
func findIndexWhere(mapping *yaml.Node, match func(literal string) bool) int {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Kind == yaml.ScalarNode && k.Tag != "!!merge" && match(k.Value) {
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

// setEnvScalar writes one env entry where it already stands, mark included, so
// an existing HeadComment stays above it; a key not yet present is appended.
func setEnvScalar(mapping *yaml.Node, name string, marked bool, val string) {
	key := envname.JoinMarked(name, marked)
	if idx := findEnvIndex(mapping, name); idx >= 0 {
		keyNode := mapping.Content[idx-1]
		if keyNode.Value != key {
			// A bool-spelled key keeps its tag until the mark changes the
			// literal; the mark makes it a plain string.
			keyNode.Value = key
			keyNode.Tag = "!!str"
		}
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

// writeMark reports whether env key k of profile name must be written with the
// secret mark. An authoritative p.Marked (non-nil) answers directly. Given no
// mark information, the file being rewritten decides: the profile's own entry
// first, then an ancestor inside this same file. A cross-layer ancestor is
// invisible here, which is why callers holding the merged config pass Marked.
// The chain lookup is best effort: a dangling extends or a cycle yields no
// mark, never a write failure.
func writeMark(root *yaml.Node, name, k string, p Profile, env *yaml.Node) bool {
	if p.Marked != nil {
		return p.Marked[k]
	}
	if idx := findEnvIndex(env, k); idx >= 0 {
		_, marked := envname.SplitMarked(env.Content[idx-1].Value)
		return marked
	}
	var c Config
	if err := root.Decode(&c); err != nil {
		return false
	}
	resolved, err := c.ResolveProfile(name)
	if err != nil {
		return false
	}
	return resolved.Marked[k]
}

// UpsertProfile merges profile `name` into the config file at path, preserving
// existing structure and comments via the YAML node tree. Creates the file (and
// parent dirs) if absent.
//
// Env keys merge additively (new values override existing same keys, matched by
// the name the secret mark is stripped from). A key's mark is rewritten from
// p.Marked when that map is non-nil, and otherwise carried over from the file
// (see writeMark). setDefault updates the top-level `default` key. p.Comments[k]
// (non-empty) sets/overrides the comment above that env entry; empty/missing
// leaves any existing comment untouched.
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

	// forceExtends writes extends even when empty: the explicit (none) choice.
	// Without it, an empty p.Extends preserves the existing extends (import
	// contract).
	if forceExtends || len(p.Extends) > 0 {
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
			setEnvScalar(env, k, writeMark(root, name, k, p, env), p.Env[k])
			if c := p.Comments[k]; c != "" {
				// idx is the value node's position; idx-1 is the key node.
				// Attaching HeadComment to the key node renders the comment on
				// the line above the KEY: entry.
				if idx := findEnvIndex(env, k); idx >= 0 {
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
	return writePath(path, out)
}
