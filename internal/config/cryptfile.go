package config

import (
	"bytes"
	"fmt"
	"os"

	"github.com/neiromaster/enver/internal/crypto"
	"gopkg.in/yaml.v3"
)

func profilesMapping(body *yaml.Node) *yaml.Node {
	idx := findIndex(body, "profiles")
	if idx < 0 || body.Content[idx].Kind != yaml.MappingNode {
		return nil
	}
	return body.Content[idx]
}

// profilesMappingOrError distinguishes an absent profiles key (nil, nil) from
// a structurally broken one (error), so the crypt paths fail the way readers
// fail on the same input.
func profilesMappingOrError(body *yaml.Node, path string) (*yaml.Node, error) {
	if pm := profilesMapping(body); pm != nil {
		return pm, nil
	}
	if findIndex(body, "profiles") >= 0 {
		return nil, fmt.Errorf("profiles is not a mapping in %s", path)
	}
	return nil, nil
}

// profileInMapping reports whether pm holds a profile named name.
func profileInMapping(pm *yaml.Node, name string) bool {
	for i := 0; i+1 < len(pm.Content); i += 2 {
		if pm.Content[i].Value == name {
			return true
		}
	}
	return false
}

// requireProfile checks the profile filter names an existing profile, so a
// typo cannot turn into a silent zero-value success. A nil pm (no profiles
// key at all) fails for any named profile.
func requireProfile(pm *yaml.Node, profile string) error {
	if profile == "" {
		return nil
	}
	if pm == nil || !profileInMapping(pm, profile) {
		return fmt.Errorf("profile %q not found", profile)
	}
	return nil
}

func envMapping(profileNode *yaml.Node) *yaml.Node {
	idx := findIndex(profileNode, "env")
	if idx < 0 || profileNode.Content[idx].Kind != yaml.MappingNode {
		return nil
	}
	return profileNode.Content[idx]
}

// forEachEnvValue calls fn for every scalar env value of every profile (only
// profile when non-empty), in file order, stopping at the first error.
func forEachEnvValue(pm *yaml.Node, profile string, fn func(v string) error) error {
	// A nil profiles mapping holds no values.
	if pm == nil {
		return nil
	}
	for i := 0; i+1 < len(pm.Content); i += 2 {
		if profile != "" && pm.Content[i].Value != profile {
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
			if err := fn(valNode.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

// sameSaltEra returns the KDF parameters of the enc:v3 values under salt
// anywhere in the file. When the profiles being written hold no encrypted
// values of their own, new values still join the era of same-salt values in
// the rest of the file: same salt plus different params derives a different
// key, so stamping CurrentParams beside an older era would split the file
// under one passphrase. Values under other salts (stranded under a lost key)
// are skipped.
func sameSaltEra(pm *yaml.Node, salt []byte) (crypto.Argon2Params, bool, error) {
	var scan crypto.SaltScan
	err := forEachEnvValue(pm, "", func(v string) error {
		if !crypto.IsEncrypted(v) {
			return nil
		}
		vSalt, _, err := crypto.SaltFromValue(v)
		if err != nil {
			return err
		}
		if !bytes.Equal(vSalt, salt) {
			return nil
		}
		return scan.Add(v)
	})
	if err != nil {
		return crypto.Argon2Params{}, false, err
	}
	if !scan.Found() {
		return crypto.Argon2Params{}, false, nil
	}
	_, p, _ := scan.Result()
	return p, true, nil
}

// EncryptFile encrypts secret-looking values (or all values when all is true)
// in the config at path, preserving structure and comments. profile filters
// to a single profile; empty means all. salt is shared by every value
// encrypted in this run: passphrase recovery derives the key from the first
// value in the file, so per-value salts would strand the rest. New values carry the KDF params of the encrypted values already under the write key — those in the profiles being written, else same-salt values elsewhere in the file (the header must describe how the key in play was derived). Values under a different salt are refused before anything is written: encrypting beside them would mix two keys and strand them. Values
// stranded under other profiles do not block the write. Values this build
// cannot read (foreign enc: prefixes, malformed enc:v3) fail loudly in every
// profile, filtered or not. Returns the count of newly encrypted values.
func EncryptFile(path string, key, salt []byte, profile string, all bool) (int, error) {
	root, err := loadOrInitRoot(path)
	if err != nil {
		return 0, err
	}
	body := root.Content[0]
	pm, err := profilesMappingOrError(body, path)
	if err != nil {
		return 0, err
	}
	if err := requireProfile(pm, profile); err != nil {
		return 0, err
	}
	if pm == nil {
		return 0, nil
	}
	if err := forEachEnvValue(pm, "", crypto.CheckReadable); err != nil {
		return 0, err
	}
	var scan crypto.SaltScan
	if err := forEachEnvValue(pm, profile, scan.Add); err != nil {
		return 0, err
	}
	fileSalt, fileParams, _ := scan.Result()
	if scan.Found() && !bytes.Equal(fileSalt, salt) {
		return 0, fmt.Errorf("existing encrypted values in %s use a different key; install the matching key (`enver keygen --force` with the passphrase that encrypted them, or restore their key file), then re-encrypt", path)
	}
	params := crypto.CurrentParams
	if scan.Found() {
		params = fileParams
	} else if era, ok, err := sameSaltEra(pm, salt); err != nil {
		return 0, err
	} else if ok {
		params = era
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
			if !all && !ShouldEncrypt(keyNode.Value, valNode.Value) {
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

// DecryptFile reverses EncryptFile for the encrypted values it finds. The key is pre-verified against the first encrypted value in the target profiles, so a wrong key fails once with the recovery path instead of mid-run. Foreign enc: values fail loudly in every profile, filtered or not. Returns the count of decrypted values.
func DecryptFile(path string, key []byte, profile string) (int, error) {
	root, err := loadOrInitRoot(path)
	if err != nil {
		return 0, err
	}
	body := root.Content[0]
	pm, err := profilesMappingOrError(body, path)
	if err != nil {
		return 0, err
	}
	if err := requireProfile(pm, profile); err != nil {
		return 0, err
	}
	if pm == nil {
		return 0, nil
	}
	if err := forEachEnvValue(pm, "", func(v string) error {
		if p := crypto.ForeignEncPrefix(v); p != "" {
			return crypto.ForeignEncError(p)
		}
		return nil
	}); err != nil {
		return 0, err
	}
	// The key is verified against the first encrypted value in the target
	// profiles before anything is decrypted: a wrong key fails once with the
	// recovery path in the message, not per value mid-run. Stranded values in
	// profiles this run does not touch stay out of scope, mirroring EncryptFile.
	var verify crypto.SaltScan
	if err := forEachEnvValue(pm, profile, verify.Add); err != nil {
		return 0, err
	}
	if verify.Found() {
		_, _, sample := verify.Result()
		if _, derr := crypto.DecryptValue(sample, key); derr != nil {
			return 0, fmt.Errorf("the key does not decrypt the existing values in %s; install the matching key (`enver keygen --force` with the passphrase that encrypted them, or restore their key file), then retry", path)
		}
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

// ScanCrypt walks every env value in the config at path through salts.Add.
// Foreign enc: values and enc:v3 values disagreeing on salt or KDF parameters
// are errors, so a scan that returns nil saw values from a single key era.
func ScanCrypt(path string, salts *crypto.SaltScan) error {
	c, err := load(path)
	if err != nil {
		return err
	}
	for _, prof := range c.Profiles {
		for _, v := range prof.Env {
			if err := salts.Add(v); err != nil {
				return err
			}
		}
	}
	return nil
}

// FirstSaltAndSample returns the salt, KDF parameters, and full value of the
// first enc:v3: value in the config at path, or a nil salt when none exists.
// Used to recover the salt and params for passphrase key derivation. Foreign
// enc: values, malformed enc:v3 values, and values disagreeing on salt or
// params are errors: recovery must not silently pick from what it cannot
// fully read.
func FirstSaltAndSample(path string) (salt []byte, p crypto.Argon2Params, sample string, err error) {
	var scan crypto.SaltScan
	if err := ScanCrypt(path, &scan); err != nil {
		return nil, crypto.Argon2Params{}, "", err
	}
	salt, p, sample = scan.Result()
	return salt, p, sample, nil
}
