package remote

import (
	"encoding/json"
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
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	// Four segments is an unpinned raw file; a fifth means the URL already
	// names a revision, and anything else is not a shape this function builds.
	if len(segments) != 4 || segments[2] != "raw" {
		return "", ""
	}
	revision := lookupGistRevision(segments[1])
	if revision == "" {
		return "", ""
	}
	pinned := gistRawOrigin + "/" + segments[0] + "/" + segments[1] + "/raw/" + revision + "/" + segments[3]
	return pinned, revision
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
