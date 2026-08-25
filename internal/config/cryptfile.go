package config

import (
	"bytes"
	"fmt"
	"os"

	"github.com/neiromaster/enver/internal/crypto"
	"gopkg.in/yaml.v3"
)

func loadNode(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("config file %s is empty", path)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config file %s is not a mapping", path)
	}
	return &root, nil
}

func profilesMapping(body *yaml.Node) *yaml.Node {
	idx := findIndex(body, "profiles")
	if idx < 0 || body.Content[idx].Kind != yaml.MappingNode {
		return nil
	}
	return body.Content[idx]
}

func envMapping(profileNode *yaml.Node) *yaml.Node {
	idx := findIndex(profileNode, "env")
	if idx < 0 || profileNode.Content[idx].Kind != yaml.MappingNode {
		return nil
	}
	return profileNode.Content[idx]
}

// forEachEnvValue calls fn for every scalar env value of every profile, in
// file order, stopping at the first error.
func forEachEnvValue(pm *yaml.Node, fn func(v string) error) error {
	for i := 0; i+1 < len(pm.Content); i += 2 {
		env := envMapping(pm.Content[i+1])
		if env == nil {
			continue
		}
		for j := 0; j+1 < len(env.Content); j += 2 {
			valNode := env.Content[j+1]
			if valNode.Kind != yaml.ScalarNode {
				continue
			}
			if err := fn(valNode.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// EncryptFile encrypts secret-looking values (or all values when all is true)
// in the config at path, preserving structure and comments. profile filters
// to a single profile; empty means all. salt is shared by every value
// encrypted in this run: passphrase recovery derives the key from the first
// value in the file, so per-value salts would strand the rest. New values
// carry the KDF params of the file's existing values when the encrypting salt
// matches theirs — the header must describe how the key in play was derived —
// and CurrentParams otherwise. Values this build cannot read (foreign enc:
// prefixes, malformed enc:v3) fail loudly in every profile, filtered or not.
// Returns the count of newly encrypted values.
func EncryptFile(path string, key, salt []byte, profile string, all bool) (int, error) {
	root, err := loadNode(path)
	if err != nil {
		return 0, err
	}
	body := root.Content[0]
	pm := profilesMapping(body)
	if pm == nil {
		return 0, nil
	}
	var scan crypto.SaltScan
	if err := forEachEnvValue(pm, scan.Add); err != nil {
		return 0, err
	}
	params := crypto.CurrentParams
	if fileSalt, fileParams, _ := scan.Result(); scan.Found() && bytes.Equal(fileSalt, salt) {
		params = fileParams
	}
	count := 0
	for i := 0; i+1 < len(pm.Content); i += 2 {
		name := pm.Content[i].Value
		if profile != "" && name != profile {
			continue
		}
		env := envMapping(pm.Content[i+1])
		if env == nil {
			continue
		}
		for j := 0; j+1 < len(env.Content); j += 2 {
			keyNode, valNode := env.Content[j], env.Content[j+1]
			if valNode.Kind != yaml.ScalarNode {
				continue
			}
			if crypto.IsEncrypted(valNode.Value) {
				continue
			}
			if !all && !IsSensitive(keyNode.Value, valNode.Value) {
				continue
			}
			enc, err := crypto.EncryptValueWithParams(valNode.Value, key, salt, params)
			if err != nil {
				return count, err
			}
			valNode.Value = enc
			valNode.Tag = "!!str"
			count++
		}
	}
	if count == 0 {
		return 0, nil
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		return count, err
	}
	return count, os.WriteFile(path, out, 0o644)
}

// DecryptFile reverses EncryptFile for the encrypted values it finds. Foreign
// enc: values fail loudly in every profile, filtered or not. Returns the
// count of decrypted values.
func DecryptFile(path string, key []byte, profile string) (int, error) {
	root, err := loadNode(path)
	if err != nil {
		return 0, err
	}
	body := root.Content[0]
	pm := profilesMapping(body)
	if pm == nil {
		return 0, nil
	}
	if err := forEachEnvValue(pm, func(v string) error {
		if p := crypto.ForeignEncPrefix(v); p != "" {
			return crypto.ForeignEncError(p)
		}
		return nil
	}); err != nil {
		return 0, err
	}
	count := 0
	for i := 0; i+1 < len(pm.Content); i += 2 {
		name := pm.Content[i].Value
		if profile != "" && name != profile {
			continue
		}
		env := envMapping(pm.Content[i+1])
		if env == nil {
			continue
		}
		for j := 0; j+1 < len(env.Content); j += 2 {
			valNode := env.Content[j+1]
			if valNode.Kind != yaml.ScalarNode {
				continue
			}
			if !crypto.IsEncrypted(valNode.Value) {
				continue
			}
			plain, err := crypto.DecryptValue(valNode.Value, key)
			if err != nil {
				return count, err
			}
			valNode.Value = plain
			valNode.Tag = "!!str"
			count++
		}
	}
	if count == 0 {
		return 0, nil
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		return count, err
	}
	return count, os.WriteFile(path, out, 0o644)
}

// FirstSaltAndSample returns the salt, KDF parameters, and full value of the
// first enc:v3: value in the config at path, or (nil, crypto.Argon2Params{},
// "", nil) when none exists. Used to recover the salt and params for
// passphrase key derivation. Foreign enc: values, malformed enc:v3 values,
// and values disagreeing on salt or params are errors: recovery must not
// silently pick from what it cannot fully read.
func FirstSaltAndSample(path string) (salt []byte, p crypto.Argon2Params, sample string, err error) {
	root, err := loadNode(path)
	if err != nil {
		return nil, crypto.Argon2Params{}, "", err
	}
	pm := profilesMapping(root.Content[0])
	if pm == nil {
		return nil, crypto.Argon2Params{}, "", nil
	}
	var scan crypto.SaltScan
	if err := forEachEnvValue(pm, scan.Add); err != nil {
		return nil, crypto.Argon2Params{}, "", err
	}
	salt, p, sample = scan.Result()
	return salt, p, sample, nil
}
