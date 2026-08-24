package config

// ReadProfile loads one profile's own env, comments, extends, and whether the
// file at path names it default. ok is false (no error) if file or profile is
// absent. An empty file is an empty config, matching the struct path.
func ReadProfile(path, name string) (p Profile, comments map[string]string, isDefault bool, ok bool, err error) {
	cfg, err := load(path)
	if err != nil {
		return Profile{}, nil, false, false, err
	}
	if cfg.Default == name {
		isDefault = true
	}
	prof, ok := cfg.Profiles[name]
	if !ok {
		return Profile{}, nil, isDefault, false, nil
	}
	return prof, prof.Comments, isDefault, true, nil
}
