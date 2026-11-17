package remote

import (
	"net/http"
	"sync/atomic"
	"testing"
)

// pointGistAt makes gist head resolution talk to origin, both as the gist raw
// host and as the API root.
func pointGistAt(t *testing.T, origin string) {
	t.Helper()
	raw := gistRawOrigin
	gistRawOrigin = origin
	t.Cleanup(func() { gistRawOrigin = raw })
	pointGitHubAPIAt(t, origin)
}

func TestGistHead(t *testing.T) {
	const (
		id  = "abc123"
		sha = "0123456789abcdef0123456789abcdef01234567"
	)
	// on and none build the table's URLs against the origin each case starts
	// its own server on; none keeps an empty want distinct from the bare
	// origin string.
	on := func(path string) func(string) string {
		return func(origin string) string { return origin + path }
	}
	none := func(string) string { return "" }

	tests := []struct {
		name       string
		source     func(origin string) string
		apiStatus  int
		apiBody    string
		wantPinned func(origin string) string
		wantRev    string
		wantHits   int64
	}{
		{
			name:       "head revision resolves",
			source:     on("/someone/" + id + "/raw/enver.yaml"),
			apiStatus:  http.StatusOK,
			apiBody:    `{"history":[{"version":"` + sha + `"}]}`,
			wantPinned: on("/someone/" + id + "/raw/" + sha + "/enver.yaml"),
			wantRev:    sha,
			wantHits:   1,
		},
		{
			name:       "file is kept",
			source:     on("/someone/" + id + "/raw/settings.yaml"),
			apiStatus:  http.StatusOK,
			apiBody:    `{"history":[{"version":"` + sha + `"}]}`,
			wantPinned: on("/someone/" + id + "/raw/" + sha + "/settings.yaml"),
			wantRev:    sha,
			wantHits:   1,
		},
		{
			name:       "API answers 500",
			source:     on("/someone/" + id + "/raw/enver.yaml"),
			apiStatus:  http.StatusInternalServerError,
			wantPinned: none,
			wantHits:   1,
		},
		{
			name:       "API body has no history",
			source:     on("/someone/" + id + "/raw/enver.yaml"),
			apiStatus:  http.StatusOK,
			apiBody:    `{}`,
			wantPinned: none,
			wantHits:   1,
		},
		{
			name:       "URL already names a revision",
			source:     on("/someone/" + id + "/raw/" + sha + "/enver.yaml"),
			wantPinned: none,
		},
		{
			name:       "URL is not a gist raw URL",
			source:     func(string) string { return "https://example.com/a/b/raw/c" },
			wantPinned: none,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int64
			mux := http.NewServeMux()
			mux.HandleFunc("/gists/"+id, func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				if got := r.URL.Path; got != "/gists/"+id {
					t.Errorf("the API was asked for %q, want %q", got, "/gists/"+id)
				}
				if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
					t.Errorf("the API request sent Accept %q, want application/vnd.github+json", got)
				}
				if tc.apiStatus != http.StatusOK {
					http.Error(w, "lookup failed", tc.apiStatus)
					return
				}
				_, _ = w.Write([]byte(tc.apiBody))
			})
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				t.Errorf("GistHead requested %q, which no case of the lookup needs", r.URL)
				http.NotFound(w, r)
			})
			srv := newTLSServer(t, mux)
			pointGistAt(t, srv.URL)
			pinned, revision := GistHead(tc.source(srv.URL))
			if want := tc.wantPinned(srv.URL); pinned != want {
				t.Errorf("GistHead pinned = %q, want %q", pinned, want)
			}
			if revision != tc.wantRev {
				t.Errorf("GistHead revision = %q, want %q", revision, tc.wantRev)
			}
			if got := hits.Load(); got != tc.wantHits {
				t.Errorf("GistHead hit the server %d times, want %d", got, tc.wantHits)
			}
		})
	}
}
