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
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const prefixV3 = "enc:v3:"

const keySize = 32

// argon2id parameters (RFC 9106).
const (
	kdfTime    = 3
	kdfMemory  = 64 * 1024 // KiB
	kdfThreads = 4
	saltSize   = 16
)

// Bounds on the KDF parameters an enc:v3 value may carry. The header is
// attacker-controllable input: passphrase recovery derives with whatever it
// says, so the bounds keep a committed config from turning each attempt into
// a multi-gigabyte, minute-long computation. maxKDFCost caps t*m, the dominant
// argon2id cost, at 16x CurrentParams.
const (
	maxKDFTime    = 32
	maxKDFMemory  = 1048576 // KiB (1 GiB)
	maxKDFThreads = 32
	maxKDFCost    = 3145728 // t*m
)

// SaltSize is the length of the argon2id salt in bytes.
const SaltSize = saltSize

// Argon2Params are the argon2id parameters carried in every enc:v3 value.
type Argon2Params struct {
	Time    uint32
	Memory  uint32 // KiB
	Threads uint8
}

// CurrentParams are the compile-time parameters new values are encrypted with.
var CurrentParams = Argon2Params{kdfTime, kdfMemory, kdfThreads}

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

// GenerateKey writes a fresh key cache (random key and salt) to path as JSON
// with 0600 perms. Refuses to overwrite an existing file unless force is true.
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
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	return WriteKeyCache(path, KeyCache{Version: 1, Salt: salt, Key: key})
}

// LoadKey reads the key cache at path, returning the key and its salt.
func LoadKey(path string) (key, salt []byte, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	c, err := parseKeyCache(data)
	if err != nil {
		return nil, nil, err
	}
	return c.Key, c.Salt, nil
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

// IsEncrypted reports whether v carries the enc:v3: prefix.
func IsEncrypted(v string) bool {
	return strings.HasPrefix(v, prefixV3)
}

// EncryptValue returns "enc:v3:argon2id:<t>:<m>:<p>:<base64(salt||nonce||ciphertext)>"
// with the current KDF parameters.
func EncryptValue(plain string, key, salt []byte) (string, error) {
	return EncryptValueWithParams(plain, key, salt, CurrentParams)
}

// EncryptValueWithParams encrypts with explicit KDF parameters embedded in the
// value header, so decryption and passphrase recovery never depend on the
// compile-time constants.
func EncryptValueWithParams(plain string, key, salt []byte, p Argon2Params) (string, error) {
	if len(salt) != saltSize {
		return "", fmt.Errorf("salt must be %d bytes, got %d", saltSize, len(salt))
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(plain), nil)
	payload := make([]byte, 0, len(salt)+len(nonce)+len(sealed))
	payload = append(payload, salt...)
	payload = append(payload, nonce...)
	payload = append(payload, sealed...)
	header := fmt.Sprintf("%sargon2id:%d:%d:%d:", prefixV3, p.Time, p.Memory, p.Threads)
	return header + base64.StdEncoding.EncodeToString(payload), nil
}

// parseV3 splits an enc:v3 value into its KDF parameters and base64 payload.
func parseV3(v string) (Argon2Params, string, error) {
	rest := strings.TrimPrefix(v, prefixV3)
	parts := strings.SplitN(rest, ":", 5)
	if len(parts) != 5 {
		return Argon2Params{}, "", errors.New("malformed enc:v3 header: want argon2id:<t>:<m>:<p>:<payload>")
	}
	if parts[0] != "argon2id" {
		return Argon2Params{}, "", fmt.Errorf("unsupported KDF %q", boundEcho(parts[0]))
	}
	t, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return Argon2Params{}, "", fmt.Errorf("malformed enc:v3 header field t: invalid number")
	}
	m, err := strconv.ParseUint(parts[2], 10, 32)
	if err != nil {
		return Argon2Params{}, "", fmt.Errorf("malformed enc:v3 header field m: invalid number")
	}
	pCount, err := strconv.ParseUint(parts[3], 10, 8)
	if err != nil {
		return Argon2Params{}, "", fmt.Errorf("malformed enc:v3 header field p: invalid number")
	}
	p := Argon2Params{Time: uint32(t), Memory: uint32(m), Threads: uint8(pCount)}
	if p.Time < 1 {
		return Argon2Params{}, "", errors.New("malformed enc:v3 header field t: must be >= 1")
	}
	if p.Time > maxKDFTime {
		return Argon2Params{}, "", fmt.Errorf("malformed enc:v3 header field t: must be <= %d", maxKDFTime)
	}
	if p.Threads < 1 {
		return Argon2Params{}, "", errors.New("malformed enc:v3 header field p: must be >= 1")
	}
	if p.Threads > maxKDFThreads {
		return Argon2Params{}, "", fmt.Errorf("malformed enc:v3 header field p: must be <= %d", maxKDFThreads)
	}
	if p.Memory < 8*uint32(p.Threads) {
		return Argon2Params{}, "", fmt.Errorf("malformed enc:v3 header field m: must be >= 8*p (%d KiB)", 8*uint32(p.Threads))
	}
	if p.Memory > maxKDFMemory {
		return Argon2Params{}, "", fmt.Errorf("malformed enc:v3 header field m: must be <= %d KiB", maxKDFMemory)
	}
	if cost := uint64(p.Time) * uint64(p.Memory); cost > maxKDFCost {
		return Argon2Params{}, "", fmt.Errorf("malformed enc:v3 header: t*m must be <= %d (got %d)", maxKDFCost, cost)
	}
	return p, parts[4], nil
}

// DecryptValue reverses EncryptValue. Returns an error if v is not an enc:v3:
// value or the key/payload are invalid.
func DecryptValue(v string, key []byte) (string, error) {
	if !strings.HasPrefix(v, prefixV3) {
		return "", errors.New("value is not encrypted")
	}
	_, payload, err := parseV3(v)
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("invalid ciphertext: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(raw) < saltSize+ns+gcm.Overhead() {
		return "", errors.New("ciphertext too short")
	}
	nonce, sealed := raw[saltSize:saltSize+ns], raw[saltSize+ns:]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decryption failed (wrong key?): %w", err)
	}
	return string(plain), nil
}

// DeriveKey derives a 32-byte AES key from a passphrase and salt with the
// given argon2id parameters.
func DeriveKey(passphrase string, salt []byte, p Argon2Params) ([]byte, error) {
	if len(salt) == 0 {
		return nil, errors.New("salt required")
	}
	return argon2.IDKey([]byte(passphrase), salt, p.Time, p.Memory, p.Threads, keySize), nil
}

// SaltFromValue extracts the salt and KDF parameters from an enc:v3 value.
// Errors for other values.
func SaltFromValue(v string) ([]byte, Argon2Params, error) {
	if !strings.HasPrefix(v, prefixV3) {
		return nil, Argon2Params{}, errors.New("value is not enc:v3")
	}
	p, payload, err := parseV3(v)
	if err != nil {
		return nil, Argon2Params{}, err
	}
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, Argon2Params{}, fmt.Errorf("invalid ciphertext: %w", err)
	}
	if len(raw) < saltSize {
		return nil, Argon2Params{}, errors.New("ciphertext too short")
	}
	return raw[:saltSize], p, nil
}

// SaltScan accumulates the salt, KDF parameters, and a sample value across
// enc:v3 values for passphrase recovery. One passphrase-derived key serves the
// whole config, so a value whose salt or parameters disagree with the first
// one recorded is a conflict, not a tie to break by map order. Foreign enc:
// values and malformed enc:v3 values are errors: recovery must not silently
// skip what it cannot read. The zero value accepts its first Add.
type SaltScan struct {
	salt   []byte
	params Argon2Params
	sample string
}

// Add records v when it is enc:v3. Plaintext values are ignored.
func (s *SaltScan) Add(v string) error {
	if p := ForeignEncPrefix(v); p != "" {
		return ForeignEncError(p)
	}
	if !IsEncrypted(v) {
		return nil
	}
	salt, params, err := SaltFromValue(v)
	if err != nil {
		return err
	}
	if s.salt == nil {
		s.salt, s.params, s.sample = salt, params, v
		return nil
	}
	if !bytes.Equal(s.salt, salt) || s.params != params {
		return errors.New("enc:v3 values disagree on salt or KDF parameters; decrypt and re-encrypt with one key")
	}
	return nil
}

// Found reports whether any enc:v3 value was recorded.
func (s *SaltScan) Found() bool {
	return s.salt != nil
}

// Result returns the recorded salt, KDF parameters, and the full sample value
// they came from; zero values when nothing was recorded.
func (s *SaltScan) Result() (salt []byte, p Argon2Params, sample string) {
	return s.salt, s.params, s.sample
}

// KeyCache is the on-disk passphrase key cache. KDF parameters live in the
// encrypted values, not here; the cache stores the already-derived key.
type KeyCache struct {
	Version int    `json:"v"`
	Salt    []byte `json:"salt"`
	Key     []byte `json:"key"`
}

// NewKeyCache builds a cache entry; KDF parameters live in the values, not
// here — the cache stores the already-derived key.
func NewKeyCache(salt, key []byte) KeyCache {
	return KeyCache{Version: 1, Salt: salt, Key: key}
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

// ForeignEncPrefix returns the enc-family prefix of a value this build cannot
// handle (for example "enc:v2:"), or "" when v is enc:v3 or not enc-family.
// enver owns the "enc:" namespace in configs.
func ForeignEncPrefix(v string) string {
	if !strings.HasPrefix(v, "enc:") || strings.HasPrefix(v, prefixV3) {
		return ""
	}
	if i := strings.IndexByte(v[4:], ':'); i >= 0 {
		return v[:4+i+1]
	}
	// No second colon: bound the echo so a long value (possibly a plaintext
	// secret) never lands whole in an error message.
	return boundEcho(v)
}

// ForeignEncError describes an enc-family value this build cannot read.
func ForeignEncError(prefix string) error {
	return fmt.Errorf("unsupported encrypted value %q; this enver build cannot read it — re-encrypt with a compatible version", prefix)
}

// boundEcho truncates a string to 16 bytes plus "..." to avoid leaking
// attacker-controlled values in error messages.
func boundEcho(s string) string {
	const maxLen = 16
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
