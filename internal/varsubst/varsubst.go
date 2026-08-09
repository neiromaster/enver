// Package varsubst resolves $VAR-style references in enver profile values. It is
// the in-process interpolation engine: no shell, no command substitution.
package varsubst

import "strings"

// expandValue expands $VAR and ${VAR} (with the :-, -, :+, + operators) in s. $$
// is a literal $. A $ that does not start a reference ($(…), $5, trailing $) is
// left literal. lookup returns the value and whether the name is set. Operands of
// braced operators are themselves expanded.
func expandValue(s string, lookup func(name string) (string, bool)) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != '$' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '$' {
			b.WriteByte('$')
			i += 2
			continue
		}
		if i+1 < len(s) && s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				b.WriteByte('$')
				i++
				continue
			}
			b.WriteString(evalBraced(s[i+2:i+2+end], lookup))
			i += 2 + end + 1
			continue
		}
		name, n := readName(s[i+1:])
		if n == 0 {
			b.WriteByte('$')
			i++
			continue
		}
		v, _ := lookup(name)
		b.WriteString(v)
		i += 1 + n
	}
	return b.String()
}

// readName reads an identifier ([A-Za-z_][A-Za-z0-9_]*) from the start of s,
// returning the name and its byte length (0 if s does not start with one).
func readName(s string) (string, int) {
	if len(s) == 0 || !isNameStart(s[0]) {
		return "", 0
	}
	n := 1
	for n < len(s) && isNamePart(s[n]) {
		n++
	}
	return s[:n], n
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
}

func isNamePart(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9')
}

// evalBraced evaluates the inside of ${...}: a name plus an optional operator
// (:-, -, :+, +) and operand. An invalid name or unknown operator is returned
// literally as ${expr}.
func evalBraced(expr string, lookup func(name string) (string, bool)) string {
	name, n := readName(expr)
	if n == 0 {
		return "${" + expr + "}"
	}
	rest := expr[n:]
	v, ok := lookup(name)
	switch {
	case rest == "":
		if ok {
			return v
		}
		return ""
	case strings.HasPrefix(rest, ":-"):
		if ok && v != "" {
			return v
		}
		return expandValue(rest[2:], lookup)
	case strings.HasPrefix(rest, "-"):
		if ok {
			return v
		}
		return expandValue(rest[1:], lookup)
	case strings.HasPrefix(rest, ":+"):
		if ok && v != "" {
			return expandValue(rest[2:], lookup)
		}
		return ""
	case strings.HasPrefix(rest, "+"):
		if ok {
			return expandValue(rest[1:], lookup)
		}
		return ""
	default:
		return "${" + expr + "}"
	}
}

// Expand resolves references across env. Each value is expanded depth-first
// against the profile's own values first, then osEnv; osEnv values are leaves
// (not re-expanded). A reference that forms a cycle (including self-reference)
// is treated as unset and so resolves to empty. The result is a new map; env and
// osEnv are not mutated.
func Expand(env, osEnv map[string]string) map[string]string {
	out := make(map[string]string, len(env))
	var resolve func(s string, visited map[string]bool) string
	lookup := func(name string, visited map[string]bool) (string, bool) {
		if v, ok := env[name]; ok {
			if visited[name] {
				return "", false // cycle: act as unset
			}
			visited[name] = true
			r := resolve(v, visited)
			delete(visited, name)
			return r, true
		}
		if v, ok := osEnv[name]; ok {
			return v, true
		}
		return "", false
	}
	resolve = func(s string, visited map[string]bool) string {
		return expandValue(s, func(name string) (string, bool) {
			return lookup(name, visited)
		})
	}
	for k, v := range env {
		out[k] = resolve(v, map[string]bool{k: true})
	}
	return out
}
