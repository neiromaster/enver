package config

import (
	"fmt"
	"slices"

	"gopkg.in/yaml.v3"
)

// Extends is the ordered list of profiles a profile inherits from. YAML accepts
// either a scalar (extends: anth) or a sequence (extends: [a, b]); both
// normalize to a slice. Order is significant: a later entry overrides an
// earlier one on a shared env key, and the profile's own env overrides parents.
type Extends []string

// Has reports whether name is among the parents.
func (e Extends) Has(name string) bool {
	return slices.Contains(e, name)
}

// UnmarshalYAML accepts extends as a scalar or a sequence, normalizing both to
// a slice. A null or empty scalar yields nil (no parents).
func (e *Extends) UnmarshalYAML(value *yaml.Node) error {
	names, err := decodeNameList(value, "extends")
	if err != nil {
		return err
	}
	*e = Extends(names)
	return nil
}

// Unsets is the list of env keys a profile removes from the resolved
// environment and from the child process environment. YAML accepts either a
// scalar (unset: API_KEY) or a sequence (unset: [A, B]); both normalize to a
// slice, mirroring extends.
type Unsets []string

// UnmarshalYAML accepts unset as a scalar or a sequence, normalizing both to a
// slice. A null or empty scalar yields nil (no unsets).
func (u *Unsets) UnmarshalYAML(value *yaml.Node) error {
	names, err := decodeNameList(value, "unset")
	if err != nil {
		return err
	}
	*u = Unsets(names)
	return nil
}

// decodeNameList normalizes a YAML scalar or sequence into a name slice; any
// other node shape is an error naming the field. A mapping (unset: {FOO:
// reason}) must not silently disable the field it was meant to configure.
func decodeNameList(value *yaml.Node, field string) ([]string, error) {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Tag == "!!null" || value.Value == "" {
			return nil, nil
		}
		return []string{value.Value}, nil
	case yaml.SequenceNode:
		var names []string
		if err := value.Decode(&names); err != nil {
			return nil, err
		}
		return names, nil
	case yaml.AliasNode:
		return decodeNameList(value.Alias, field)
	default:
		return nil, fmt.Errorf("line %d: %s must be a single name or a list of names", value.Line, field)
	}
}

// writeExtendsNode writes extends into prof: omitted when empty, a scalar when
// single, a sequence when multiple. Mirrors the accepted read forms.
func writeExtendsNode(prof *yaml.Node, extends Extends) {
	writeNameListNode(prof, "extends", extends)
}

// writeUnsetNode writes unset into prof with the same shape rules as extends.
func writeUnsetNode(prof *yaml.Node, unset Unsets) {
	writeNameListNode(prof, "unset", unset)
}

func writeNameListNode(prof *yaml.Node, key string, names []string) {
	switch len(names) {
	case 0:
		removeKey(prof, key)
	case 1:
		setScalar(prof, key, names[0])
	default:
		setSequence(prof, key, names)
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
