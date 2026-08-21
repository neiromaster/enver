package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/argon2"
)

const (
	prefixV1 = "enc:v1:"
	prefixV2 = "enc:v2:"
)

const keySize = 32

// argon2id parameters (RFC 9106).
const (
	kdfTime    = 3
	kdfMemory  = 64 * 1024 // KiB
	kdfThreads = 4
	saltSize   = 16
)

// SaltSize is the length of the argon2id salt in bytes.
const SaltSize = saltSize

// KeyFilePath is the default location for the enver key file. A var so tests
// can redirect it.
var KeyFilePath = func() string {
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

// LoadKey reads the key at path, accepting either a passphrase cache (JSON) or
// a legacy raw base64 key.
func LoadKey(path string) ([]byte, error) {
	key, _, err := LoadKeyWithSalt(path)
	return key, err
}

// LoadKeyWithSalt reads the key at path, returning the salt when the file is a
// passphrase cache (nil for legacy raw keys).
func LoadKeyWithSalt(path string) (key, salt []byte, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		c, err := parseKeyCache(trimmed)
		if err != nil {
			return nil, nil, err
		}
		return c.Key, c.Salt, nil
	}
	k, err := DecodeKey(string(trimmed))
	return k, nil, err
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

// IsEncrypted reports whether v carries an enver encrypted-value prefix.
func IsEncrypted(v string) bool {
	return hasPrefix(v, prefixV1) || hasPrefix(v, prefixV2)
}

// EncryptValue returns "enc:v1:<base64>" when salt is nil, or
// "enc:v2:<base64(salt||nonce||ciphertext)>" when salt is non-nil.
func EncryptValue(plain string, key []byte, salt ...[]byte) (string, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plain), nil)
	if len(salt) == 0 || salt[0] == nil {
		payload := make([]byte, 0, len(nonce)+len(sealed))
		payload = append(payload, nonce...)
		payload = append(payload, sealed...)
		return prefixV1 + base64.StdEncoding.EncodeToString(payload), nil
	}
	if len(salt[0]) != saltSize {
		return "", fmt.Errorf("salt must be %d bytes, got %d", saltSize, len(salt[0]))
	}
	payload := make([]byte, 0, len(salt[0])+len(nonce)+len(sealed))
	payload = append(payload, salt[0]...)
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	return prefixV2 + base64.StdEncoding.EncodeToString(payload), nil
}

// DecryptValue reverses EncryptValue for both enc:v1: and enc:v2: values.
// Returns an error if v is not encrypted or the key/payload are invalid.
func DecryptValue(v string, key []byte) (string, error) {
	var p string
	switch {
	case hasPrefix(v, prefixV1):
		p = prefixV1
	case hasPrefix(v, prefixV2):
		p = prefixV2
	default:
		return "", errors.New("value is not encrypted")
	}
	raw, err := base64.StdEncoding.DecodeString(v[len(p):])
	if err != nil {
		return "", fmt.Errorf("invalid ciphertext: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	off := 0
	if p == prefixV2 {
		if len(raw) < saltSize+ns+gcm.Overhead() {
			return "", errors.New("ciphertext too short")
		}
		off = saltSize
	} else if len(raw) < ns+gcm.Overhead() {
		return "", errors.New("ciphertext too short")
	}
	nonce, sealed := raw[off:off+ns], raw[off+ns:]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (wrong key?): %w", err)
	}
	return string(plain), nil
}

// DeriveKey derives a 32-byte AES key from a passphrase and salt using argon2id.
func DeriveKey(passphrase string, salt []byte) ([]byte, error) {
	if len(salt) == 0 {
		return nil, errors.New("salt required")
	}
	return argon2.IDKey([]byte(passphrase), salt, kdfTime, kdfMemory, kdfThreads, keySize), nil
}

// SaltFromValue extracts the salt from an enc:v2: value. Errors for non-v2
// values.
func SaltFromValue(v string) ([]byte, error) {
	if !hasPrefix(v, prefixV2) {
		return nil, errors.New("value is not enc:v2")
	}
	raw, err := base64.StdEncoding.DecodeString(v[len(prefixV2):])
	if err != nil {
		return nil, fmt.Errorf("invalid ciphertext: %w", err)
	}
	if len(raw) < saltSize {
		return nil, errors.New("ciphertext too short")
	}
	return raw[:saltSize], nil
}

// KeyCache is the on-disk passphrase key cache.
type KeyCache struct {
	Version int    `json:"v"`
	KDF     string `json:"kdf"`
	Time    uint32 `json:"t"`
	Memory  uint32 `json:"m"`
	Threads uint8  `json:"p"`
	Salt    []byte `json:"salt"`
	Key     []byte `json:"key"`
}

// NewKeyCache builds a cache entry with the current KDF parameters.
func NewKeyCache(salt, key []byte) KeyCache {
	return KeyCache{
		Version: 1,
		KDF:     "argon2id",
		Time:    kdfTime,
		Memory:  kdfMemory,
		Threads: kdfThreads,
		Salt:    salt,
		Key:     key,
	}
}

// WriteKeyCache writes c to path as JSON with 0600 perms.
func WriteKeyCache(path string, c KeyCache) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadKeyCache reads and parses a KeyCache from path.
func LoadKeyCache(path string) (KeyCache, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return KeyCache{}, err
	}
	return parseKeyCache(data)
}

func parseKeyCache(data []byte) (KeyCache, error) {
	var c KeyCache
	if err := json.Unmarshal(data, &c); err != nil {
		return KeyCache{}, fmt.Errorf("invalid key cache: %w", err)
	}
	if len(c.Key) != keySize {
		return KeyCache{}, fmt.Errorf("invalid key cache: key length %d, want %d", len(c.Key), keySize)
	}
	if len(c.Salt) != saltSize {
		return KeyCache{}, fmt.Errorf("invalid key cache: salt length %d, want %d", len(c.Salt), saltSize)
	}
	return c, nil
}

func hasPrefix(s, p string) bool {
	return len(s) > len(p) && s[:len(p)] == p
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
