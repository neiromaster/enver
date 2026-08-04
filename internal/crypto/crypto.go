package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const prefix = "enc:v1:"

const keySize = 32

// KeyFilePath is the default location for the enver key file.
func KeyFilePath() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "enver", "key")
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "/"
	}
	return filepath.Join(home, ".config", "enver", "key")
}

// GenerateKey writes a fresh random key (base64) to path with 0600 perms.
// Refuses to overwrite an existing file unless force is true.
func GenerateKey(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("key already exists at %s (use --force to overwrite)", path)
		}
	}
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(key)), 0o600)
}

// LoadKey reads and base64-decodes the key at path.
func LoadKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return DecodeKey(string(data))
}

// DecodeKey base64-decodes an inline key string (e.g. from ENVER_KEY).
func DecodeKey(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid key (expected base64): %w", err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("invalid key length: got %d bytes, want %d", len(key), keySize)
	}
	return key, nil
}

// IsEncrypted reports whether v carries the enver encrypted-value prefix.
func IsEncrypted(v string) bool {
	return len(v) > len(prefix) && v[:len(prefix)] == prefix
}

// EncryptValue returns "enc:v1:<base64>" for plain under key.
func EncryptValue(plain string, key []byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plain), nil)
	payload := make([]byte, 0, len(nonce)+len(sealed))
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	return prefix + base64.StdEncoding.EncodeToString(payload), nil
}

// DecryptValue reverses EncryptValue. Returns an error if v is not encrypted
// or the key/payload are invalid.
func DecryptValue(v string, key []byte) (string, error) {
	if !IsEncrypted(v) {
		return "", errors.New("value is not encrypted")
	}
	raw, err := base64.StdEncoding.DecodeString(v[len(prefix):])
	if err != nil {
		return "", fmt.Errorf("invalid ciphertext: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(raw) < ns+gcm.Overhead() {
		return "", errors.New("ciphertext too short")
	}
	nonce, sealed := raw[:ns], raw[ns:]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (wrong key?): %w", err)
	}
	return string(plain), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
