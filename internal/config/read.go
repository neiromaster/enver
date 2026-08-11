package config

import "os"

// ReadProfile loads one profile's own env (values and the HeadComment above each
// key), its extends, and whether the file at path names it as the default. It
// reflects only what is in that one file. ok is false (with no error) when the
// file or profile is absent.
func ReadProfile(path, name string) (p Profile, comments map[string]string, isDefault bool, ok bool, err error) {
	root, err := loadNode(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Profile{}, nil, false, false, nil
		}
		return Profile{}, nil, false, false, err
	}
	body := root.Content[0]

	if dv := findIndex(body, "default"); dv >= 0 && body.Content[dv].Value == name {
		isDefault = true
	}

	pm := profilesMapping(body)
	if pm == nil {
		return Profile{}, nil, isDefault, false, nil
	}
	idx := findIndex(pm, name)
	if idx < 0 {
		return Profile{}, nil, isDefault, false, nil
	}

	profNode := pm.Content[idx]
	p = Profile{Env: map[string]string{}}
	if ev := findIndex(profNode, "extends"); ev >= 0 {
		p.Extends = readExtendsNode(profNode.Content[ev])
	}
	env := envMapping(profNode)
	if env != nil {
		comments = map[string]string{}
		for i := 0; i+1 < len(env.Content); i += 2 {
			keyNode, valNode := env.Content[i], env.Content[i+1]
			p.Env[keyNode.Value] = valNode.Value
			if c := keyNode.HeadComment; c != "" {
				if len(c) >= 2 && c[0] == '#' && c[1] == ' ' {
					c = c[2:]
				}
				comments[keyNode.Value] = c
			}
		}
	}
	return p, comments, isDefault, true, nil
}
