package config

import (
	"errors"

	"gopkg.in/yaml.v3"
)

// FileKind is what a file handed to `enver validate` turns out to be.
type FileKind int

const (
	// FileNotEnver carries neither the publish mark nor any enver section.
	FileNotEnver FileKind = iota
	// FileLocal has an enver section and no mark.
	FileLocal
	// FilePublish carries `publish: true`.
	FilePublish
)

// InspectFile parses data as one enver document and reports what it is, the
// top-level keys enver does not know, and an error when the document is not
// YAML, its publish mark is not the boolean true, or a profile carries the
// mark. A document that is not a mapping is not an enver file, not an error.
// A top-level merge key is followed: keys arriving through `<<:` say what the
// file is just as spelled-out keys do.
func InspectFile(data []byte) (kind FileKind, unknownKeys []string, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return FileNotEnver, nil, err
	}
	root := firstRoot(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return FileNotEnver, nil, nil
	}
	if err := publishInsideProfile(root); err != nil {
		return FileNotEnver, nil, err
	}
	s := fileScan{mergeLeft: countMergeKeys(root)}
	s.mapping(root)
	mark := deref(s.mark)
	if s.hasMark && mark.Tag != "!!bool" {
		return FileNotEnver, nil, errors.New("publish: want the boolean true")
	}
	var marked bool
	if s.hasMark {
		if err := mark.Decode(&marked); err != nil {
			return FileNotEnver, nil, err
		}
		if marked {
			return FilePublish, s.unknown, nil
		}
	}
	if !s.hasSection {
		return FileNotEnver, nil, nil
	}
	return FileLocal, s.unknown, nil
}

// fileScan collects what one pass over a document's keys says: which enver
// sections are present, where the publish mark sits, and the keys enver does
// not know.
type fileScan struct {
	unknown    []string
	hasSection bool
	hasMark    bool
	mark       *yaml.Node
	mergeLeft  int
}

// mapping walks one mapping's keys as top-level keys. Merge keys are
// followed, so `<<:` at the top level brings the anchored mapping's keys into
// the file's top level.
func (s *fileScan) mapping(m *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		key, val := m.Content[i], m.Content[i+1]
		if key.Tag == "!!merge" {
			s.mergeValue(val)
			continue
		}
		switch key.Value {
		case "profiles", "remotes", "default":
			s.hasSection = true
		case "publish":
			s.hasMark, s.mark = true, val
		default:
			s.unknown = append(s.unknown, key.Value)
		}
	}
}

// mergeValue scans the mapping one merge key's value names: an alias to a
// mapping, a mapping spelled in place, or a sequence of either. The walk
// follows merge keys only — no general alias expansion — and the budget of the
// file's own merge keys bounds it, so a merge loop cannot walk forever.
func (s *fileScan) mergeValue(val *yaml.Node) {
	switch val.Kind {
	case yaml.AliasNode:
		if s.mergeLeft <= 0 {
			return
		}
		s.mergeLeft--
		s.mapping(val.Alias)
	case yaml.MappingNode:
		s.mapping(val)
	case yaml.SequenceNode:
		for _, item := range val.Content {
			s.mergeValue(item)
		}
	}
}

// countMergeKeys counts the merge keys anywhere in the tree, the budget the
// merge walk is bounded by.
func countMergeKeys(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	count := 0
	if n.Tag == "!!merge" {
		count++
	}
	for _, child := range n.Content {
		count += countMergeKeys(child)
	}
	return count
}

// publishInsideProfile reports the first profile that spells a publish key:
// the mark is read only at the top level, so inside a profile it is ignored,
// and the file would be judged local while its author plainly meant to
// publish.
func publishInsideProfile(root *yaml.Node) error {
	var profiles *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "profiles" {
			profiles = deref(root.Content[i+1])
			break
		}
	}
	if profiles == nil || profiles.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(profiles.Content); i += 2 {
		p := deref(profiles.Content[i+1])
		if p.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(p.Content); j += 2 {
			if p.Content[j].Value == "publish" {
				return errors.New("publish is not a profile field; the mark belongs at the top level of the file")
			}
		}
	}
	return nil
}

// deref resolves an alias node to the node it names; every other node is
// returned as itself, and so is nil.
func deref(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.AliasNode && n.Alias != nil {
		return n.Alias
	}
	return n
}

// firstRoot returns the single node a document node wraps, or nil for the zero
// node an empty or comment-only document decodes to.
func firstRoot(doc yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}
