package config

import "gopkg.in/yaml.v3"

// Extends is the ordered list of profiles a profile inherits from. YAML accepts
// either a scalar (extends: anth) or a sequence (extends: [a, b]); both
// normalize to a slice. Order is significant: a later entry overrides an
// earlier one on a shared env key, and the profile's own env overrides parents.
type Extends []string

// Has reports whether name is among the parents.
func (e Extends) Has(name string) bool {
	for _, x := range e {
		if x == name {
			return true
		}
	}
	return false
}

// UnmarshalYAML accepts extends as a scalar or a sequence, normalizing both to
// a slice. A null or empty scalar yields nil (no parents).
func (e *Extends) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Tag == "!!null" || value.Value == "" {
			return nil
		}
		*e = Extends{value.Value}
	case yaml.SequenceNode:
		var names []string
		if err := value.Decode(&names); err != nil {
			return err
		}
		*e = Extends(names)
	case yaml.AliasNode:
		return value.Alias.Decode(e)
	}
	return nil
}

// readExtendsNode reads an extends node (scalar or sequence) from the raw YAML
// tree into a slice, mirroring UnmarshalYAML for the node-tree paths
// (ReadProfile, comments).
func readExtendsNode(n *yaml.Node) Extends {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" || n.Value == "" {
			return nil
		}
		return Extends{n.Value}
	case yaml.SequenceNode:
		var names []string
		if err := n.Decode(&names); err != nil {
			return nil
		}
		return Extends(names)
	case yaml.AliasNode:
		return readExtendsNode(n.Alias)
	}
	return nil
}

// writeExtendsNode writes extends into prof: omitted when empty, a scalar when
// single, a sequence when multiple. Mirrors the accepted read forms.
func writeExtendsNode(prof *yaml.Node, extends Extends) {
	switch len(extends) {
	case 0:
		removeKey(prof, "extends")
	case 1:
		setScalar(prof, "extends", extends[0])
	default:
		setSequence(prof, "extends", extends)
	}
}

// setSequence sets mapping[key] to a sequence of the given string values,
// replacing any existing entry.
func setSequence(mapping *yaml.Node, key string, vals []string) {
	seq := &yaml.Node{Kind: yaml.SequenceNode}
	for _, v := range vals {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: v, Tag: "!!str"})
	}
	if idx := findIndex(mapping, key); idx >= 0 {
		mapping.Content[idx] = seq
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		seq,
	)
}
