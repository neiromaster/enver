package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// maxBodyBytes is the hard cap on a fetched config. A config is a few KiB;
	// a response past this is refused, not buffered.
	maxBodyBytes = 8 << 20
	maxRedirects = 10
	fetchTimeout = 30 * time.Second
	// commitTimeout bounds the best-effort commit lookup, so a slow API costs a
	// pull seconds, not the fetch.
	commitTimeout = 10 * time.Second
)

var (
	// ErrCrossHostRedirect reports a redirect to any other host than the one the
	// source names: the user registered one host, and a fetch does not wander.
	ErrCrossHostRedirect = errors.New("remote source redirected across hosts")
	// ErrDowngrade reports a redirect that would fetch an https source over
	// plain http.
	ErrDowngrade = errors.New("remote source redirected to plain http")
	// ErrTooLarge reports a body past maxBodyBytes.
	ErrTooLarge = fmt.Errorf("remote source body exceeds the %d MiB cap", maxBodyBytes>>20)

	// githubRawOrigin and githubAPIOrigin are the GitHub endpoints commit
	// resolution talks to; tests point them at an httptest server.
	githubRawOrigin = "https://raw.githubusercontent.com"
	githubAPIOrigin = "https://api.github.com"
	// fetchTransport is the transport behind both clients Fetch builds; tests
	// set it to an httptest TLS server's transport. A nil transport means
	// http.DefaultTransport.
	fetchTransport http.RoundTripper
)

// Fetch downloads the remote config at rawURL, which is fetched exactly as the
// source resolved it: ref is never appended to it. A ref of "" resolves
// nothing — a plain https source has no ref to resolve and a github URL already
// carries its ref in its path.
//
// The commit is the sha an unpinned github source resolved to. It stays empty
// whenever no resolution applies or the lookup fails: a pull must survive a
// rate-limited API with its content intact.
//
// Only https is fetched, a hop to another host or to plain http fails the fetch
// with ErrCrossHostRedirect or ErrDowngrade, and a body past maxBodyBytes fails
// it with ErrTooLarge.
func Fetch(rawURL, ref string) ([]byte, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("remote source %q is not a URL: %w", rawURL, err)
	}
	if u.Scheme != "https" {
		return nil, "", fmt.Errorf("remote source %q is not https; enver fetches remotes over https only", rawURL)
	}
	client := &http.Client{
		Timeout:       fetchTimeout,
		Transport:     fetchTransport,
		CheckRedirect: redirectPolicy(u),
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("fetch %s: %s", rawURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", rawURL, err)
	}
	if len(body) > maxBodyBytes {
		return nil, "", fmt.Errorf("%w: %s", ErrTooLarge, rawURL)
	}
	return body, resolveCommit(u, ref), nil
}

// redirectPolicy admits only hops that stay on the host the source names and on
// https, so an https source never lands on a host the user did not register or
// on a connection anyone can rewrite.
func redirectPolicy(origin *url.URL) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("%s redirects more than %d times", origin, maxRedirects)
		}
		switch req.URL.Scheme {
		case "https":
		case "http":
			return fmt.Errorf("%w: %s redirects to %s", ErrDowngrade, origin, req.URL)
		default:
			return fmt.Errorf("remote source %s redirects to scheme %q: %s", origin, req.URL.Scheme, req.URL)
		}
		if req.URL.Host != origin.Host {
			return fmt.Errorf("%w: %s redirects to %s", ErrCrossHostRedirect, origin, req.URL)
		}
		return nil
	}
}

// resolveCommit returns the sha a raw github HEAD source resolved to. A tag or
// sha ref already names what it serves, a source on another host has no
// repository to ask, and a ref that disagrees with the URL segment is not the
// commit of what was fetched.
func resolveCommit(u *url.URL, ref string) string {
	if ref != "HEAD" || u.Scheme+"://"+u.Host != githubRawOrigin {
		return ""
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) < 3 || segments[2] != ref {
		return ""
	}
	return lookupCommit(segments[0], segments[1])
}

// lookupCommit asks the GitHub API for the commit HEAD points at, returning ""
// on any failure: a rate limit costs the pull its commit metadata, never its
// content.
func lookupCommit(owner, repo string) string {
	origin, err := url.Parse(githubAPIOrigin)
	if err != nil {
		return ""
	}
	apiURL := githubAPIOrigin + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/commits/HEAD"
	client := &http.Client{
		Timeout:       commitTimeout,
		Transport:     fetchTransport,
		CheckRedirect: redirectPolicy(origin),
	}
	req, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var payload struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&payload); err != nil {
		return ""
	}
	return payload.SHA
}
