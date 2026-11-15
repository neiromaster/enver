// Package remote resolves and fetches the config files a remotes: section
// registers.
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

// ParseSource resolves a source into the URL to fetch and the ref that URL
// pins. A github: source takes owner/repo, an optional path (default
// enver.yaml at the repo root) and an optional @ref (default HEAD), and
// resolves to the raw.githubusercontent.com file URL. An https source is
// fetched as written, with a trailing @ref split off; plain http is refused,
// since a source may not arrive over a connection anyone can rewrite.
func ParseSource(raw string) (string, string, error) {
	src, err := parseSource(raw)
	if err != nil {
		return "", "", err
	}
	return src.resolve("")
}

// ResolveRecord resolves a remotes record into the URL to fetch and the ref
// it pins. The record's ref wins over one spelled inline in its URL: pull
// splits an inline ref into the record when the remote is registered, but a
// hand-written config can still spell both.
func ResolveRecord(rec config.RemoteRecord) (string, string, error) {
	src, err := parseSource(rec.URL)
	if err != nil {
		return "", "", err
	}
	return src.resolve(rec.Ref)
}

type sourceKind int

const (
	githubSource sourceKind = iota
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
	body, ref, hasRef := splitRef(rest)
	if hasRef && ref == "" {
		return source{}, fmt.Errorf("source %q has an empty ref", raw)
	}
	segments := strings.Split(body, "/")
	if len(segments) < 2 {
		return source{}, fmt.Errorf("source %q is not github:owner/repo[/path][@ref]", raw)
	}
	for _, segment := range segments {
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
	base, ref, hasRef := splitRef(raw)
	if hasRef && ref == "" {
		return source{}, fmt.Errorf("source %q has an empty ref", raw)
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
		ref = override
	}
	if ref != "" && !tokenRe.MatchString(ref) {
		return "", "", fmt.Errorf("source %q pins ref %q, which a source cannot carry; a ref takes letters, digits, . _ + ~ -", s.raw, ref)
	}
	if s.kind == urlSource {
		return s.base, ref, nil
	}
	if ref == "" {
		ref = githubDefaultRef
	}
	return strings.Join(append([]string{githubRawURL, s.owner, s.repo, ref}, s.path...), "/"), ref, nil
}

func splitScheme(raw string) (scheme, rest string, ok bool) {
	m := schemeRe.FindStringSubmatch(raw)
	if m == nil {
		return "", "", false
	}
	return strings.ToLower(m[1]), raw[len(m[0]):], true
}

// splitRef peels a trailing @ref off a source. A ref is one path segment, so
// an @ sitting before the last slash belongs to the source text instead: a
// URL's userinfo, or a directory in a github path.
func splitRef(s string) (head, ref string, ok bool) {
	i := strings.LastIndexByte(s, '@')
	if i < 0 || i < strings.LastIndexByte(s, '/') {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}
