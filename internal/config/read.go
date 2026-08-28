package config

// ReadProfile loads one profile from the file at path, including its comments
// and whether the file names it default. ok is false (no error) if file or
// profile is absent. An empty file is an empty config, matching the struct path.
func ReadProfile(path, name string) (p Profile, isDefault bool, ok bool, err error) {
	cfg, err := load(path)
	if err != nil {
		return Profile{}, false, false, err
	}
	if cfg.Default == name {
		isDefault = true
	}
	prof, ok := cfg.Profiles[name]
	if !ok {
		return Profile{}, isDefault, false, nil
	}
	return prof, isDefault, true, nil
}
