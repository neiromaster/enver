package config

import (
	"runtime"
	"strings"
)

// EnvKeyEqual reports whether two env var names denote the same key on this
// platform: byte-exact on POSIX systems, case-insensitive on Windows, where
// the environment is case-preserving but case-insensitive (Path vs PATH).
// Every match of an unset against an env key goes through it, so a
// case-mismatched unset cannot silently no-op on Windows.
func EnvKeyEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// DeleteEnvKey removes key from m by EnvKeyEqual semantics.
func DeleteEnvKey(m map[string]string, key string) {
	deleteEnvKey(m, key)
}

// deleteEnvKey is the shape-generic core of DeleteEnvKey: env, comments, and
// sources maps all strip fenced keys with the same matching rules.
func deleteEnvKey[V any](m map[string]V, key string) {
	if runtime.GOOS == "windows" {
		for k := range m {
			if EnvKeyEqual(k, key) {
				delete(m, k)
				return
			}
		}
	}
	delete(m, key)
}

// hasEnvKey reports whether m carries key by EnvKeyEqual semantics.
func hasEnvKey(m map[string]string, key string) bool {
	if _, ok := m[key]; ok {
		return true
	}
	if runtime.GOOS == "windows" {
		for k := range m {
			if EnvKeyEqual(k, key) {
				return true
			}
		}
	}
	return false
}

// UnsetsHasKey reports whether key is fenced by any entry of unsets.
func UnsetsHasKey(unsets []string, key string) bool {
	for _, u := range unsets {
		if EnvKeyEqual(u, key) {
			return true
		}
	}
	return false
}

// SetEnvKey sets m[key] = val, first removing any case-variant of key on
// Windows, where PATH and Path denote one variable: an exact-key assignment
// would leave both spellings in the map, and the fence would then have to
// delete both. POSIX is a plain assignment.
func SetEnvKey(m map[string]string, key, val string) {
	setEnvKeyed(m, key, val)
}

// setEnvKeyed is the shape-generic core of SetEnvKey: env, comments, sources,
// and origins maps all overlay with the same case rules.
func setEnvKeyed[V any](m map[string]V, key string, val V) {
	if runtime.GOOS == "windows" {
		for k := range m {
			if EnvKeyEqual(k, key) {
				delete(m, k)
				break
			}
		}
	}
	m[key] = val
}

// ValidEnvKey reports whether k is a name enver accepts for an env key or an
// unset entry: [A-Za-z_][A-Za-z0-9_]* — the identifier rule the dotenv
// parser has always enforced on import. Names reach eval'd export lines and
// child environments unquoted, so a looser rule is code execution, and one
// rule everywhere keeps hand-authored YAML and imported .env files in the
// same namespace.
func ValidEnvKey(k string) bool {
	if k == "" || !envKeyStart(k[0]) {
		return false
	}
	for i := 1; i < len(k); i++ {
		if !envKeyPart(k[i]) {
			return false
		}
	}
	return true
}

func envKeyStart(c byte) bool { return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func envKeyPart(c byte) bool  { return envKeyStart(c) || (c >= '0' && c <= '9') }
