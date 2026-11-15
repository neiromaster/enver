// Package remote resolves the sources a remotes: section registers into the
// URL and ref to fetch.
package remote

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/neiromaster/enver/internal/config"
)

const (
	githubRawURL      = "https://raw.githubusercontent.com"
	githubDefaultPath = "enver.yaml"
	githubDefaultRef  = "HEAD"
)

var (
	// schemeRe matches an RFC 3986 scheme prefix, so a scheme the registry
	// does not know is told apart from a source that has none at all.
	schemeRe = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9+.-]*):`)
	// tokenRe matches one token that lands in the resolved URL's path without
	// escaping: a github owner, repo or path segment, and a ref, which is a
	// path segment of the raw URL a pinned github source resolves to.
	tokenRe = regexp.MustCompile(`^[A-Za-z0-9_.+~-]+$`)
)

// ParseSource resolves a source into the URL to fetch and the ref it pins.
//
// A github: source takes owner/repo, an optional path (default enver.yaml at
// the repo root) and an optional @ref (default HEAD), and resolves to the
// raw.githubusercontent.com file URL. The ref is a segment of that URL, so the
// returned ref restates a segment already inside the returned URL: append it
// nowhere.
//
// An https source is fetched exactly as written with a trailing @ref split off.
// There the ref is a label: accepted and stored, compared by the divergence
// check, reported by pull, never applied to the URL — what such a source serves
// is whatever the address serves. Plain http is refused: a source may not
// arrive over a connection anyone can rewrite.
func ParseSource(raw string) (string, string, error) {
	src, err := parseSource(raw)
	if err != nil {
		return "", "", err
	}
	return src.resolve("")
}

// ResolveRecord resolves a remotes record into the URL to fetch and the ref it
// pins. The record's ref wins over one spelled inline in its URL: pull splits
// an inline ref into the record when the remote is registered, but a
// hand-written config can still spell both. As in ParseSource, a github ref
// lands inside the returned URL while a plain https ref stays a label.
func ResolveRecord(rec config.RemoteRecord) (string, string, error) {
	src, err := parseSource(rec.URL)
	if err != nil {
		return "", "", err
	}
	return src.resolve(rec.Ref)
}

// sourceKind is the form a source was written in. The zero value is no form, so
// a source that was never parsed cannot resolve into a github URL.
type sourceKind int

const (
	githubSource sourceKind = iota + 1
	urlSource
)

// source is a parsed source before its defaults are filled: the parts the
// form contributes, plus the ref spelled inline, if any.
type source struct {
	raw   string
	kind  sourceKind
	base  string   // urlSource: the URL with the inline ref peeled off
	owner string   // githubSource
	repo  string   // githubSource
	path  []string // githubSource: the file path inside the repo
	ref   string   // spelled inline in raw, "" when absent
}

func parseSource(raw string) (source, error) {
	scheme, rest, ok := splitScheme(raw)
	if !ok {
		return source{}, fmt.Errorf("source %q has no scheme; want the github:owner/repo or https:// form", raw)
	}
	switch scheme {
	case "github":
		return parseGitHubSource(raw, rest)
	case "https":
		return parseURLSource(raw)
	case "http":
		return source{}, fmt.Errorf("source %q is plain http; enver fetches sources over https only", raw)
	default:
		return source{}, fmt.Errorf("source %q uses scheme %q, which enver does not resolve; want the github:owner/repo or https:// form", raw, scheme)
	}
}

func parseGitHubSource(raw, rest string) (source, error) {
	body, ref, _, err := splitRef(raw, rest)
	if err != nil {
		return source{}, err
	}
	segments := strings.Split(body, "/")
	if len(segments) < 2 {
		return source{}, fmt.Errorf("source %q is not github:owner/repo[/path][@ref]", raw)
	}
	for _, segment := range segments {
		if segment == "." || segment == ".." {
			return source{}, fmt.Errorf("source %q has a %q segment; a source names one file in the repo, it does not move around it", raw, segment)
		}
		if !tokenRe.MatchString(segment) {
			return source{}, fmt.Errorf("source %q has an unusable segment %q; an owner, repo and path take letters, digits, . _ + ~ -", raw, segment)
		}
	}
	path := segments[2:]
	if len(path) == 0 {
		path = []string{githubDefaultPath}
	}
	return source{raw: raw, kind: githubSource, owner: segments[0], repo: segments[1], path: path, ref: ref}, nil
}

func parseURLSource(raw string) (source, error) {
	base, ref, _, err := splitPathRef(raw)
	if err != nil {
		return source{}, err
	}
	u, err := url.Parse(base)
	if err != nil {
		return source{}, fmt.Errorf("source %q is not a URL enver can fetch: %w", raw, err)
	}
	if u.Host == "" {
		return source{}, fmt.Errorf("source %q has no host", raw)
	}
	return source{raw: raw, kind: urlSource, base: base, ref: ref}, nil
}

// resolve fills the defaults and returns the URL to fetch with the ref it
// pins. override is the record's ref, which wins over one spelled inline.
func (s source) resolve(override string) (string, string, error) {
	ref := s.ref
	if override != "" {
		if err := validateRef(s.raw, override); err != nil {
			return "", "", err
		}
		ref = override
	}
	switch s.kind {
	case urlSource:
		return s.base, ref, nil
	case githubSource:
		if ref == "" {
			ref = githubDefaultRef
		}
		return strings.Join(append([]string{githubRawURL, s.owner, s.repo, ref}, s.path...), "/"), ref, nil
	default:
		return "", "", fmt.Errorf("source %q has no form", s.raw)
	}
}

// validateRef admits the refs every source form can carry. A ref becomes a path
// segment of the raw URL a github source resolves to, and travels into sidecar
// state and fetch metadata everywhere else, so one grammar covers both: no
// slash, no whitespace, nothing that changes URL structure.
func validateRef(raw, ref string) error {
	if ref == "" {
		return fmt.Errorf("source %q has an empty ref", raw)
	}
	if !tokenRe.MatchString(ref) {
		return fmt.Errorf("source %q pins ref %q, which a source cannot carry; a ref takes letters, digits, . _ + ~ -", raw, ref)
	}
	return nil
}

func splitScheme(raw string) (scheme, rest string, ok bool) {
	m := schemeRe.FindStringSubmatch(raw)
	if m == nil {
		return "", "", false
	}
	return strings.ToLower(m[1]), raw[len(m[0]):], true
}

// splitRef peels an inline @ref off the end of s. The ref is whatever follows
// the last @, unless that @ opens a segment: a path can hold @types or a
// directory named after an address, and the head before an @ inside a segment
// is read as such a directory. An @ inside a segment that goes on past a slash
// is the case a ref and a directory cannot be told apart in, so the ref reading
// is taken and validateRef refuses it rather than let pull silently fetch the
// ref as path.
func splitRef(raw, s string) (head, ref string, has bool, err error) {
	i := strings.LastIndexByte(s, '@')
	if i <= 0 || s[i-1] == '/' {
		return s, "", false, nil
	}
	ref = s[i+1:]
	if err = validateRef(raw, ref); err != nil {
		return "", "", false, err
	}
	return s[:i], ref, true, nil
}

// splitPathRef splits an inline ref off a URL path. Only the path can hold a
// ref: an @ in the userinfo, the query or the fragment belongs to the address,
// so those sources pass through whole.
func splitPathRef(raw string) (head, ref string, has bool, err error) {
	_, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return raw, "", false, nil
	}
	i := strings.IndexAny(rest, "/?#")
	if i < 0 || rest[i] != '/' {
		return raw, "", false, nil
	}
	start := len(raw) - len(rest) + i
	path := rest[i:]
	if j := strings.IndexAny(path, "?#"); j >= 0 {
		path = path[:j]
	}
	head, ref, has, err = splitRef(raw, path)
	if err != nil || !has {
		return raw, "", false, err
	}
	return raw[:start+len(head)] + raw[start+len(path):], ref, true, nil
}
