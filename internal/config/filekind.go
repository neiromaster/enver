package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// FileKind is what a file handed to `enver validate` turns out to be.
type FileKind int

const (
	// FileNotEnver carries neither the publish mark nor any enver section.
	FileNotEnver FileKind = iota
	// FileLocal has an enver section and no publish mark.
	FileLocal
	// FilePublish carries `publish: true`.
	FilePublish
)

// InspectFile parses data as one enver document and reports what it is, the
// top-level keys enver does not know, and an error when the document is not
// YAML, is not a mapping, or its publish mark is not the boolean true.
func InspectFile(data []byte) (kind FileKind, unknownKeys []string, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return FileNotEnver, nil, err
	}
	root := firstRoot(doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return FileNotEnver, nil, nil
	}
	var hasSection, hasMark bool
	var mark *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i], root.Content[i+1]
		if key.Tag == "!!merge" {
			continue
		}
		switch key.Value {
		case "profiles", "remotes", "default":
			hasSection = true
		case "publish":
			hasMark, mark = true, val
		default:
			unknownKeys = append(unknownKeys, key.Value)
		}
	}
	if hasMark && mark.Tag != "!!bool" {
		return FileNotEnver, nil, fmt.Errorf("publish: want the boolean true")
	}
	var marked bool
	if hasMark {
		if err := mark.Decode(&marked); err != nil {
			return FileNotEnver, nil, err
		}
		if marked {
			return FilePublish, unknownKeys, nil
		}
	}
	if !hasSection {
		return FileNotEnver, nil, nil
	}
	return FileLocal, unknownKeys, nil
}

// firstRoot returns the single node a document node wraps, or nil for the zero
// node an empty or comment-only document decodes to.
func firstRoot(doc yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}
