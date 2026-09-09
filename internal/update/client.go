package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	// DefaultAPIBase is the GitHub REST API root used by enver update.
	// Tests override it with a mock server.
	DefaultAPIBase = "https://api.github.com"
	// DefaultDownloadBase is the root of goreleaser release assets.
	DefaultDownloadBase = "https://github.com"
	// retryBackoff is the sleep before retry N (1-based). Overridable so a
	// failing-server test stays fast.
	retryBackoff = 400 * time.Millisecond
)

// repo is the owner/repo release metadata and assets are fetched from.
const repo = "neiromaster/enver"

// Client fetches release metadata and assets from GitHub.
type Client struct {
	apiBase      string
	downloadBase string
	http         *http.Client
	token        string
}

// NewClient returns a Client for the given API and download bases.
func NewClient(apiBase, downloadBase string) *Client {
	return &Client{
		apiBase:      apiBase,
		downloadBase: downloadBase,
		http:         &http.Client{Timeout: 10 * time.Second},
		token:        os.Getenv("GITHUB_TOKEN"),
	}
}

// LatestVersion returns the v-prefixed tag of the latest release.
func (c *Client) LatestVersion() (string, error) {
	body, err := c.get(c.apiBase + "/repos/" + repo + "/releases/latest")
	if err != nil {
		return "", err
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("parse release response: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("release response has no tag_name")
	}
	return canonicalVersion(rel.TagName), nil
}

// Checksums returns the SHA-256 checksums of a release keyed by filename.
func (c *Client) Checksums(tag string) (map[string]string, error) {
	body, err := c.get(c.downloadURL(tag, "checksums.txt"))
	if err != nil {
		return nil, err
	}
	sums := make(map[string]string)
	for _, line := range strings.Split(string(body), "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			sums[f[1]] = f[0]
		}
	}
	if len(sums) == 0 {
		return nil, fmt.Errorf("checksums.txt is empty")
	}
	return sums, nil
}

// Download returns the raw bytes of a release asset.
func (c *Client) Download(tag, asset string) ([]byte, error) {
	return c.get(c.downloadURL(tag, asset))
}

func (c *Client) downloadURL(tag, asset string) string {
	return c.downloadBase + "/" + repo + "/releases/download/" + tag + "/" + asset
}

// get fetches url with up to 3 attempts and exponential backoff, sending an
// Authorization header when GITHUB_TOKEN is set.
func (c *Client) get(url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(retryBackoff * time.Duration(1<<(attempt-1)))
		}
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("GET %s: %s", url, resp.Status)
			continue
		}
		return body, nil
	}
	return nil, lastErr
}
