package update

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// testServer spins up an httptest server rooted at the API-relative paths the
// Client calls and returns its base URL.
func testServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestLatestVersion(t *testing.T) {
	base := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/neiromaster/enver/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `{"tag_name":"v0.9.1"}`)
	}))
	got, err := NewClient(base, base).LatestVersion()
	if err != nil {
		t.Fatal(err)
	}
	if got != "v0.9.1" {
		t.Errorf("LatestVersion = %q, want v0.9.1", got)
	}
}

func TestLatestVersionRetriesThenFails(t *testing.T) {
	hits := 0
	base := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	old := retryBackoff
	retryBackoff = time.Millisecond
	t.Cleanup(func() { retryBackoff = old })

	if _, err := NewClient(base, base).LatestVersion(); err == nil {
		t.Fatal("want error after 3 failed attempts")
	}
	if hits != 3 {
		t.Errorf("attempts = %d, want 3", hits)
	}
}

func TestLatestVersionMissingTag(t *testing.T) {
	base := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{}`)
	}))
	if _, err := NewClient(base, base).LatestVersion(); err == nil {
		t.Fatal("want error when tag_name is absent")
	}
}

func TestChecksumsAndDownload(t *testing.T) {
	const (
		tag     = "v0.9.1"
		archive = "enver_0.9.1_darwin_arm64.tar.gz"
	)
	base := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/neiromaster/enver/releases/download/v0.9.1/checksums.txt":
			_, _ = fmt.Fprintf(w, "abc123  %s\n", archive)
		case "/neiromaster/enver/releases/download/v0.9.1/" + archive:
			_, _ = fmt.Fprint(w, "binary-data")
		default:
			http.NotFound(w, r)
		}
	}))
	c := NewClient(base, base)

	sums, err := c.Checksums(tag)
	if err != nil {
		t.Fatal(err)
	}
	if sums[archive] != "abc123" {
		t.Errorf("Checksums[%s] = %q, want abc123", archive, sums[archive])
	}

	data, err := c.Download(tag, archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "binary-data" {
		t.Errorf("Download = %q, want binary-data", data)
	}
}
