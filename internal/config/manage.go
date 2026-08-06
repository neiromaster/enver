package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// RenameProfile renames profile old to new in the file, rewriting every
// `extends: old` reference (in all profiles) and the top-level `default` if it
// equals old. It refuses if new already exists or old is absent. No-op if old ==
// new (and the profile exists).
func RenameProfile(path, old, new string) error {
	if old == new {
		return nil
	}
	root, err := loadNode(path)
	if err != nil {
		return err
	}
	body := root.Content[0]
	pm := profilesMapping(body)
	if pm == nil {
		return fmt.Errorf("profile %q not found", old)
	}
	if findIndex(pm, new) >= 0 {
		return fmt.Errorf("profile %q already exists", new)
	}
	idx := findIndex(pm, old)
	if idx < 0 {
		return fmt.Errorf("profile %q not found", old)
	}
	pm.Content[idx-1].Value = new

	for i := 0; i+1 < len(pm.Content); i += 2 {
		profNode := pm.Content[i+1]
		if ev := findIndex(profNode, "extends"); ev >= 0 && profNode.Content[ev].Value == old {
			profNode.Content[ev].Value = new
		}
	}

	if dv := findIndex(body, "default"); dv >= 0 && body.Content[dv].Value == old {
		body.Content[dv].Value = new
	}

	out, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return writePath(path, out)
}
