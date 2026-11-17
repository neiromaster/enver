package remote

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// gistRawOrigin is the host an unpinned gist raw source resolves against;
// tests point it at an httptest server, as with githubRawOrigin.
var gistRawOrigin = "https://gist.githubusercontent.com"

// GistHead pins a gist raw source to the revision the gist serves now. The
// bare raw URL tracks the gist, so the revision is what turns a head source
// into one whose content a later pull can compare against, and pull records
// it as the fixed point the user is on.
//
// Both results come back empty when rawURL is not an unpinned gist raw URL,
// or the lookup fails: a rate-limited or down API costs a pull its recorded
// revision, never its content. The lookup never retries.
func GistHead(rawURL string) (string, string) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme+"://"+u.Host != gistRawOrigin {
		return "", ""
	}
	owner, id, file, ok := gistRawShape(u)
	if !ok {
		return "", ""
	}
	revision := lookupGistRevision(id)
	if revision == "" {
		return "", ""
	}
	pinned := gistRawOrigin + "/" + owner + "/" + id + "/raw/" + revision + "/" + file
	return pinned, revision
}

// gistRawShape splits a gist raw URL of the owner/id/raw/file shape — four
// segments is an unpinned raw file; a fifth means the URL already names a
// revision, and anything else is not a shape a gist knows how to act on.
func gistRawShape(u *url.URL) (owner, id, file string, ok bool) {
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) != 4 || segments[2] != "raw" {
		return "", "", "", false
	}
	return segments[0], segments[1], segments[3], true
}

// IsGistPage reports whether the URL is a gist's web page rather than a file.
// The page is HTML the gate always refuses, so it is the one shape a pull can
// recognize as a mis-spelled source instead of a bad upstream.
func IsGistPage(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && u.Scheme == "https" && u.Host == "gist.github.com"
}

// gistNotFoundHint completes a fetch error for the one miss a gist raw URL can
// be: the file segment is the part a user guesses, and a wrong guess reads as a
// missing gist. Any other status, host, or shape describes itself fine already.
func gistNotFoundHint(status int, u *url.URL) string {
	if status != http.StatusNotFound || u.Scheme+"://"+u.Host != gistRawOrigin {
		return ""
	}
	owner, id, _, ok := gistRawShape(u)
	if !ok {
		return ""
	}
	return fmt.Sprintf("; the gist may name its file differently — gist:%s/%s/<file> selects it", owner, id)
}

// lookupGistRevision asks the gist API which revision the gist serves now,
// returning "" on any failure: a rate limit or an outage costs the pull its
// recorded revision, never its content.
func lookupGistRevision(id string) string {
	origin, err := url.Parse(githubAPIOrigin)
	if err != nil {
		return ""
	}
	apiURL := githubAPIOrigin + "/gists/" + url.PathEscape(id)
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
		History []struct {
			Version string `json:"version"`
		} `json:"history"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&payload); err != nil {
		return ""
	}
	if len(payload.History) == 0 {
		return ""
	}
	return payload.History[0].Version
}
