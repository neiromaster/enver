package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/neiromaster/enver/internal/envname"
	"github.com/neiromaster/enver/internal/remote/source"
	"gopkg.in/yaml.v3"
)

// requireMappingRoot rejects a parsed document whose root is not a mapping,
// so every reader and writer fails identically on the same input.
func requireMappingRoot(path string, root *yaml.Node) error {
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("config file %s is not a mapping", path)
	}
	return nil
}

// loadOrInitRoot reads the YAML document at path into a node tree, synthesising an
// empty mapping document when the file is missing or empty. It is the shared
// entry point for every node-tree access.
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
		if err := requireMappingRoot(path, &root); err != nil {
			return nil, err
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
// clears the top-level default. Each key's mark comes from p.Marked when that
// map is non-nil, and otherwise from the entry the file already holds (see
// writeMark). p.Comments[k] (non-empty) renders above that entry; a key with no
// comment is written with no comment. Creates the file (and parent dirs) if
// absent.
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
	marks := newMarkResolver(root, name, p.Marked)

	writeExtendsNode(prof, p.Extends)
	writeUnsetNode(prof, p.Unset)

	switch {
	case len(p.Env) == 0:
		removeKey(prof, "env")
	default:
		env := ensureMappingEntry(prof, "env")
		keys := make([]string, 0, len(p.Env))
		for k := range p.Env {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		// Every mark is resolved against the entries still in place; clearing
		// the mapping first would erase the only record of them.
		keyMarks := make(map[string]bool, len(keys))
		for _, k := range keys {
			keyMarks[k] = marks.writeMark(k, env)
		}
		env.Content = nil
		for _, k := range keys {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: envname.JoinMarked(k, keyMarks[k]), Tag: "!!str"}
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

// WriteRemotes replaces the config file's remotes: section wholesale: records
// absent from remotes are removed, the rest are written in sorted name order,
// and every other section, comment, and key of the file is left as it stands. An
// empty map removes the section. The write goes through a temp file and a
// rename, so a pull that dies mid-write leaves the previous section.
//
// Every record is validated first, since the section is what a later pull acts
// on: a name a pull could never use, two names differing only in case (they name
// one file on a case-insensitive filesystem), a name reserved on Windows, or a
// source the resolver cannot read would otherwise land in the config and only
// fail on the next pull.
func WriteRemotes(path string, remotes map[string]RemoteRecord) error {
	if err := ValidateRemotes(remotes); err != nil {
		return err
	}
	root, err := loadOrInitRoot(path)
	if err != nil {
		return err
	}
	body := root.Content[0]
	if len(remotes) == 0 {
		removeKey(body, "remotes")
	} else {
		section := ensureMappingEntry(body, "remotes")
		section.Content = nil
		for _, name := range slices.Sorted(maps.Keys(remotes)) {
			section.Content = append(section.Content, remoteEntry(name, remotes[name])...)
		}
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, out, 0o644)
}

// ValidateRemotes rejects a record set the pull and load paths could not use,
// with every message naming the record it refuses. It is the write-side twin of
// the load checks, run before anything is written so a bad set leaves the config
// untouched.
func ValidateRemotes(remotes map[string]RemoteRecord) error {
	seen := make(map[string]string, len(remotes))
	for _, name := range slices.Sorted(maps.Keys(remotes)) {
		if !ValidRecordName(name) {
			return fmt.Errorf("remote name %q is not a valid record name (want [A-Za-z0-9][A-Za-z0-9_-]*)", name)
		}
		if reservedDeviceName(name) {
			return fmt.Errorf("remote name %q is a reserved Windows device name; a remote name becomes a file name", name)
		}
		folded := strings.ToLower(name)
		if other, ok := seen[folded]; ok {
			return fmt.Errorf("remote names %q and %q differ only in case; a remote name becomes a file name, and the filesystem may not tell them apart", other, name)
		}
		seen[folded] = name
		rec := remotes[name]
		if rec.URL == "" {
			return fmt.Errorf("remote %q: url is required", name)
		}
		if _, _, err := source.ResolveRecord(rec.URL, rec.Ref); err != nil {
			return fmt.Errorf("remote %q: %w", name, err)
		}
	}
	return nil
}

// reservedDeviceName reports a name Windows reserves for a device whatever
// directory it sits in, so a content file of that name cannot be created. The
// record-name rule forbids a dot, so the extension forms cannot occur.
func reservedDeviceName(name string) bool {
	switch strings.ToLower(name) {
	case "con", "prn", "aux", "nul":
		return true
	}
	for _, prefix := range []string{"com", "lpt"} {
		rest, ok := strings.CutPrefix(strings.ToLower(name), prefix)
		if ok && len(rest) == 1 && rest[0] >= '1' && rest[0] <= '9' {
			return true
		}
	}
	return false
}

// remoteEntry builds the key and mapping nodes for one record.
func remoteEntry(name string, rec RemoteRecord) []*yaml.Node {
	entry := &yaml.Node{Kind: yaml.MappingNode}
	entry.Content = append(entry.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: "url", Tag: "!!str"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: rec.URL, Tag: "!!str"},
	)
	if rec.Ref != "" {
		entry.Content = append(entry.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "ref", Tag: "!!str"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: rec.Ref, Tag: "!!str"},
		)
	}
	if rec.NoPrefix {
		entry.Content = append(entry.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "no_prefix", Tag: "!!str"},
			&yaml.Node{Kind: yaml.ScalarNode, Value: "true", Tag: "!!bool"},
		)
	}
	return []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: name, Tag: "!!str"},
		entry,
	}
}

// DeleteProfile removes the named profile from the file. Missing file or profile
// is a no-op (no error). If the removed profile was the file's default, the
// default key is cleared too — a dangling default is never valid, and a config
// without a default is a legitimate state.
func DeleteProfile(path, name string) error {
	root, err := loadOrInitRoot(path)
	if err != nil {
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
