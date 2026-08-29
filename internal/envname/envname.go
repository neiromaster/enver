// Package envname holds the platform-aware env var name semantics shared by
// config, ui, dotenv, runner, and the commands: equality, map operations with
// case folding, fence-list matching, and the validity rule. A leaf package on
// purpose — nothing here may import the rest of enver.
package envname

import (
	"runtime"
	"slices"
	"strings"
)

// Equal reports whether two env var names denote the same key on this
// platform: byte-exact on POSIX, case-insensitive on Windows, where the
// environment is case-preserving but case-insensitive (Path vs PATH). Every
// match of an unset against an env key goes through it, so a case-mismatched
// unset cannot silently no-op on Windows.
func Equal(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Has reports whether m carries key by Equal semantics, so readers see
// resolutions the way resolveEnv writes them.
func Has[V any](m map[string]V, key string) bool {
	if _, ok := m[key]; ok {
		return true
	}
	if runtime.GOOS == "windows" {
		for k := range m {
			if Equal(k, key) {
				return true
			}
		}
	}
	return false
}

// Get finds m[key] by Equal semantics: direct hit anywhere, plus a Windows
// case-variant scan, mirroring how env maps are matched.
func Get[V any](m map[string]V, key string) (V, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	if runtime.GOOS == "windows" {
		for k, v := range m {
			if Equal(k, key) {
				return v, true
			}
		}
	}
	var zero V
	return zero, false
}

// Delete removes key from m by Equal semantics. On Windows every case-variant
// is removed — PATH and Path are one variable, so an unset of either spelling
// must not leave the other alive.
func Delete[V any](m map[string]V, key string) {
	if runtime.GOOS == "windows" {
		for k := range m {
			if Equal(k, key) {
				delete(m, k)
			}
		}
		return
	}
	delete(m, key)
}

// Set sets m[key] = val, first removing any case-variant of key on Windows,
// where PATH and Path denote one variable: an exact-key assignment would
// leave both spellings in the map, and the fence would then have to delete
// both. POSIX is a plain assignment.
func Set[V any](m map[string]V, key string, val V) {
	if runtime.GOOS == "windows" {
		for k := range m {
			if Equal(k, key) {
				delete(m, k)
			}
		}
	}
	m[key] = val
}

// MatchesAny reports whether key matches any entry of list by Equal
// semantics — the fence-list shape (unset entries against env keys).
func MatchesAny(list []string, key string) bool {
	for _, e := range list {
		if Equal(e, key) {
			return true
		}
	}
	return false
}

// SortedUnion returns the keys present in either map, sorted, each exact
// spelling once. The display renderers union their live and unset key sets
// with it; Resolved keeps those maps Equal-disjoint, so a duplicated spelling
// can only appear if that invariant is broken — collapsed here instead of
// rendered twice.
func SortedUnion[V, W any](a map[string]V, b map[string]W) []string {
	out := make([]string, 0, len(a)+len(b))
	for k := range a {
		out = append(out, k)
	}
	for k := range b {
		if _, dup := a[k]; !dup {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// Valid reports whether k is a name enver accepts for an env key or an unset
// entry: [A-Za-z_][A-Za-z0-9_]* — the identifier rule the dotenv parser has
// always enforced on import. Names reach eval'd export lines and child
// environments unquoted, so a looser rule is code execution, and one rule
// everywhere keeps hand-authored YAML and imported .env files in the same
// namespace.
func Valid(k string) bool {
	if k == "" || !validStart(k[0]) {
		return false
	}
	for i := 1; i < len(k); i++ {
		if !validPart(k[i]) {
			return false
		}
	}
	return true
}

func validStart(c byte) bool { return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') }
func validPart(c byte) bool  { return validStart(c) || (c >= '0' && c <= '9') }
