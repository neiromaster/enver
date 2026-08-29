package dotenv

import (
	"strings"

	"github.com/neiromaster/enver/internal/envname"
)

// Entry is one parsed KEY=VALUE pair with the comment block that immediately
// preceded it (lines joined with newlines), if any.
type Entry struct {
	Key     string
	Value   string
	Comment string
}

// Skip is one input line Parse could not turn into an entry.
type Skip struct {
	Line   int    // 1-based physical line number
	Reason string // "invalid key name" or "missing ="
}

func validKey(k string) bool { return envname.Valid(k) }

type parseError struct{ msg string }

func (e parseError) Error() string { return e.msg }

// Parse decodes dotenvx-compatible .env bytes into entries. Values are stored raw
// (interpolation is a runtime concern). The quote type drives the dotenvx bridge to
// enver's $$ literal-dollar: single-quoted and backtick values are literal and have
// each $ doubled to $$; double-quoted values process C-style escapes, turn \$ into
// $$, and leave $VAR/${...} intact; bare values are literal with an inline
// (whitespace-then-#) comment stripped. Single, double, and backtick quotes may span
// physical lines. Consecutive comment lines above a KEY (no blank between) attach to
// it; a blank line resets. A leading export is stripped. Invalid keys and lines
// without = are skipped and reported in skips. Tombstone rows enver itself emits
// (`# KEY=  # unset by "profile"`, the unset rendering of Format) are recognized
// and dropped: renderer metadata, re-derived from the target profile's own chain
// at the next export, never stored as comments.
func Parse(data []byte) (entries []Entry, skips []Skip, err error) {
	var pending []string
	var open byte // open quote for a multi-line value, else 0
	var key, comment string
	var val strings.Builder

	closeEntry := func() {
		entries = append(entries, Entry{Key: key, Value: val.String(), Comment: comment})
		key, comment = "", ""
		val.Reset()
	}

	for i, raw := range strings.Split(string(data), "\n") {
		lineNo := i + 1
		line := strings.TrimRight(raw, "\r")
		if open != 0 {
			val.WriteByte('\n')
			closed, err := consumeQuote(&val, open, line)
			if err != nil {
				return nil, nil, err
			}
			if closed {
				closeEntry()
				open = 0
			}
			pending = nil
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			pending = nil
		case strings.HasPrefix(trimmed, "#"):
			if _, tomb := parseTombstone(trimmed); tomb {
				continue
			}
			pending = append(pending, strings.TrimSpace(trimmed[1:]))
		default:
			body := strings.TrimPrefix(trimmed, "export ")
			k, rest, ok := strings.Cut(body, "=")
			if !ok {
				skips = append(skips, Skip{Line: lineNo, Reason: "missing ="})
				pending = nil
				continue
			}
			key = strings.TrimSpace(k)
			if !validKey(key) {
				skips = append(skips, Skip{Line: lineNo, Reason: "invalid key name"})
				pending = nil
				continue
			}
			comment = strings.Join(pending, "\n")
			pending = nil
			trimmed := strings.TrimLeft(rest, " \t")
			// If there was leading whitespace and the first non-whitespace char is #,
			// it's an empty value with an inline comment (dotenvx behavior).
			if trimmed != rest && len(trimmed) > 0 && trimmed[0] == '#' {
				closeEntry()
				continue
			}
			q, err := beginValue(&val, trimmed)
			if err != nil {
				return nil, nil, err
			}
			if q == 0 {
				closeEntry()
			} else {
				open = q
			}
		}
	}
	if open != 0 {
		return nil, nil, parseError{"unterminated " + string(open) + " quote"}
	}
	return entries, skips, nil
}

// beginValue writes the first line of a value to val and returns the open quote
// byte if the quote did not close on this line (0 otherwise).
func beginValue(val *strings.Builder, v string) (byte, error) {
	if v == "" {
		return 0, nil
	}
	switch v[0] {
	case '\'':
		return scanSingleContent(val, v[1:]), nil
	case '"':
		return scanDoubleContent(val, v[1:])
	case '`':
		return scanBackContent(val, v[1:]), nil
	default:
		val.WriteString(parseBare(v))
		return 0, nil
	}
}

// consumeQuote continues an open quote on a continuation line; returns whether it
// closed on this line.
func consumeQuote(val *strings.Builder, open byte, line string) (bool, error) {
	switch open {
	case '\'':
		return scanSingleContent(val, line) == 0, nil
	case '`':
		return scanBackContent(val, line) == 0, nil
	case '"':
		q, err := scanDoubleContent(val, line)
		return q == 0, err
	}
	return true, nil
}

// scanSingleContent appends single-quoted content (literal, $ -> $$) until a closing quote; returns the open-quote byte if none was found (multi-line open).
func scanSingleContent(val *strings.Builder, s string) byte {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) && s[i+1] == '\'' {
			val.WriteByte('\'')
			i++
			continue
		}
		if c == '\'' {
			return 0
		}
		writeLit(val, c)
	}
	return '\''
}

func scanBackContent(val *strings.Builder, s string) byte {
	for i := 0; i < len(s); i++ {
		if s[i] == '`' {
			return 0
		}
		writeLit(val, s[i])
	}
	return '`'
}

// scanDoubleContent appends double-quoted content (escapes processed, \$ -> $$,
// $VAR/${...} left intact) until a closing "; returns '"' if none was found.
func scanDoubleContent(val *strings.Builder, s string) (byte, error) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			writeDoubleEscape(val, s[i+1])
			i++
			continue
		}
		if c == '"' {
			return 0, nil
		}
		val.WriteByte(c)
	}
	return '"', nil
}

// writeLit writes a literal char for single/backtick context: $ -> $$.
func writeLit(val *strings.Builder, c byte) {
	if c == '$' {
		val.WriteString("$$")
	} else {
		val.WriteByte(c)
	}
}

func writeDoubleEscape(val *strings.Builder, e byte) {
	switch e {
	case 'n':
		val.WriteByte('\n')
	case 't':
		val.WriteByte('\t')
	case 'r':
		val.WriteByte('\r')
	case '\\':
		val.WriteByte('\\')
	case '"':
		val.WriteByte('"')
	case '\'':
		val.WriteByte('\'')
	case '$':
		val.WriteString("$$")
	default:
		val.WriteByte(e)
	}
}

// parseBare: literal until an inline comment (whitespace then #) or end.
func parseBare(v string) string {
	for i := 0; i < len(v); i++ {
		if v[i] == '#' && i > 0 && (v[i-1] == ' ' || v[i-1] == '\t') {
			return strings.TrimRight(v[:i], " \t")
		}
	}
	return strings.TrimRight(v, " \t")
}
