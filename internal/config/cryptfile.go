package config

import (
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

// EncryptFile encrypts secret-looking values (or all values when all is true)
// in the config at path, preserving structure and comments. profile filters
// to a single profile; empty means all. salt is shared by every value
// encrypted in this run: passphrase recovery derives the key from the first
// value in the file, so per-value salts would strand the rest. Returns the
// count of newly encrypted values.
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
			if p := crypto.ForeignEncPrefix(valNode.Value); p != "" {
				return count, crypto.ForeignEncError(p)
			}
			if crypto.IsEncrypted(valNode.Value) {
				continue
			}
			if !all && !IsSensitive(keyNode.Value, valNode.Value) {
				continue
			}
			enc, err := crypto.EncryptValue(valNode.Value, key, salt)
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

// DecryptFile reverses EncryptFile for the encrypted values it finds. Returns
// the count of decrypted values.
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
			if p := crypto.ForeignEncPrefix(valNode.Value); p != "" {
				return count, crypto.ForeignEncError(p)
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
// passphrase key derivation.
func FirstSaltAndSample(path string) (salt []byte, p crypto.Argon2Params, sample string, err error) {
	root, err := loadNode(path)
	if err != nil {
		return nil, crypto.Argon2Params{}, "", err
	}
	pm := profilesMapping(root.Content[0])
	if pm == nil {
		return nil, crypto.Argon2Params{}, "", nil
	}
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
			if s, params, err := crypto.SaltFromValue(valNode.Value); err == nil {
				return s, params, valNode.Value, nil
			}
		}
	}
	return nil, crypto.Argon2Params{}, "", nil
}
