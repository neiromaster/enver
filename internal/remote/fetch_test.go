package remote

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const testConfigBody = "profiles:\n  llm:\n    env:\n      HOST: localhost\n"

// newTLSServer starts an https server Fetch can reach and trusts its
// certificate. Every Fetch test server is TLS because Fetch refuses plain http.
func newTLSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	prev := fetchTransport
	fetchTransport = srv.Client().Transport
	t.Cleanup(func() { fetchTransport = prev })
	return srv
}

// pointGitHubAt makes commit resolution talk to origin, both as the raw source
// host and as the API root.
func pointGitHubAt(t *testing.T, origin string) {
	t.Helper()
	raw, api := githubRawOrigin, githubAPIOrigin
	githubRawOrigin, githubAPIOrigin = origin, origin
	t.Cleanup(func() { githubRawOrigin, githubAPIOrigin = raw, api })
}

// pointGitHubAPIAt makes only the API root local, so a test can observe a
// lookup that must not happen.
func pointGitHubAPIAt(t *testing.T, origin string) {
	t.Helper()
	api := githubAPIOrigin
	githubAPIOrigin = origin
	t.Cleanup(func() { githubAPIOrigin = api })
}

func TestFetchOK(t *testing.T) {
	srv := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RequestURI(); got != "/configs/enver.yaml" {
			t.Errorf("Fetch requested %q, want the source URL untouched with no ref appended", got)
		}
		_, _ = fmt.Fprint(w, testConfigBody)
	}))
	body, commit, err := Fetch(srv.URL+"/configs/enver.yaml", "v2")
	if err != nil {
		t.Fatalf("Fetch err = %v", err)
	}
	if string(body) != testConfigBody {
		t.Fatalf("Fetch body = %q, want %q", body, testConfigBody)
	}
	if commit != "" {
		t.Fatalf("Fetch commit = %q, want empty for a plain https source", commit)
	}
}

func TestFetchSameHostRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/old.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/new.yaml", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/new.yaml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, testConfigBody)
	})
	srv := newTLSServer(t, mux)
	body, _, err := Fetch(srv.URL+"/old.yaml", "")
	if err != nil {
		t.Fatalf("Fetch err = %v", err)
	}
	if string(body) != testConfigBody {
		t.Fatalf("Fetch body = %q, want the redirect target's %q", body, testConfigBody)
	}
}

func TestFetchCrossHostRedirect(t *testing.T) {
	var reached atomic.Bool
	elsewhere := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		_, _ = fmt.Fprint(w, testConfigBody)
	}))
	t.Cleanup(elsewhere.Close)
	srv := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/c.yaml", http.StatusFound)
	}))
	_, _, err := Fetch(srv.URL+"/c.yaml", "")
	if !errors.Is(err, ErrCrossHostRedirect) {
		t.Fatalf("Fetch err = %v, want ErrCrossHostRedirect", err)
	}
	if !strings.Contains(err.Error(), elsewhere.URL) {
		t.Fatalf("Fetch err = %v, want it to carry the refused URL %q", err, elsewhere.URL)
	}
	if reached.Load() {
		t.Fatal("Fetch contacted the host it refused")
	}
}

func TestFetchHTTPDowngrade(t *testing.T) {
	srv := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://"+r.Host+"/c.yaml")
		w.WriteHeader(http.StatusFound)
	}))
	_, _, err := Fetch(srv.URL+"/c.yaml", "")
	if !errors.Is(err, ErrDowngrade) {
		t.Fatalf("Fetch err = %v, want ErrDowngrade", err)
	}
}

func TestFetchRefusesPlainHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Fetch requested an http source")
	}))
	t.Cleanup(srv.Close)
	_, _, err := Fetch(srv.URL+"/c.yaml", "")
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("Fetch err = %v, want a refusal naming https", err)
	}
}

func TestFetchTooLarge(t *testing.T) {
	srv := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for written := 0; written <= maxBodyBytes; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	_, _, err := Fetch(srv.URL+"/big.yaml", "")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Fetch err = %v, want ErrTooLarge", err)
	}
}

func TestFetchSizeCapBoundary(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		wantErr error
	}{
		{name: "at the cap", size: maxBodyBytes},
		{name: "one byte over the cap", size: maxBodyBytes + 1, wantErr: ErrTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := bytes.Repeat([]byte("x"), tc.size)
			srv := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(payload)
			}))
			body, _, err := Fetch(srv.URL+"/c.yaml", "")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Fetch err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Fetch err = %v, want a %d-byte body fetched", err, tc.size)
			}
			if len(body) != tc.size {
				t.Fatalf("Fetch body = %d bytes, want %d", len(body), tc.size)
			}
		})
	}
}

func TestFetchResolvesCommit(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	mux := http.NewServeMux()
	mux.HandleFunc("/u/r/HEAD/enver.yaml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, testConfigBody)
	})
	mux.HandleFunc("/repos/u/r/commits/HEAD", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"sha":%q}`, sha)
	})
	srv := newTLSServer(t, mux)
	pointGitHubAt(t, srv.URL)
	body, commit, err := Fetch(srv.URL+"/u/r/HEAD/enver.yaml", "HEAD")
	if err != nil {
		t.Fatalf("Fetch err = %v", err)
	}
	if string(body) != testConfigBody {
		t.Fatalf("Fetch body = %q, want %q", body, testConfigBody)
	}
	if commit != sha {
		t.Fatalf("Fetch commit = %q, want %q", commit, sha)
	}
}

func TestFetchCommitLookupFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/u/r/HEAD/enver.yaml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, testConfigBody)
	})
	mux.HandleFunc("/repos/u/r/commits/HEAD", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	})
	srv := newTLSServer(t, mux)
	pointGitHubAt(t, srv.URL)
	body, commit, err := Fetch(srv.URL+"/u/r/HEAD/enver.yaml", "HEAD")
	if err != nil {
		t.Fatalf("Fetch err = %v, want the fetch to survive a failed lookup", err)
	}
	if string(body) != testConfigBody {
		t.Fatalf("Fetch body = %q, want %q", body, testConfigBody)
	}
	if commit != "" {
		t.Fatalf("Fetch commit = %q, want empty for a failed lookup", commit)
	}
}

func TestFetchCommitLookupRefusesDowngrade(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"sha":%q}`, sha)
	}))
	t.Cleanup(plain.Close)
	mux := http.NewServeMux()
	mux.HandleFunc("/u/r/HEAD/enver.yaml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, testConfigBody)
	})
	mux.HandleFunc("/repos/u/r/commits/HEAD", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", plain.URL+"/repos/u/r/commits/HEAD")
		w.WriteHeader(http.StatusFound)
	})
	srv := newTLSServer(t, mux)
	pointGitHubAt(t, srv.URL)
	body, commit, err := Fetch(srv.URL+"/u/r/HEAD/enver.yaml", "HEAD")
	if err != nil {
		t.Fatalf("Fetch err = %v, want the fetch to survive a refused lookup redirect", err)
	}
	if string(body) != testConfigBody {
		t.Fatalf("Fetch body = %q, want %q", body, testConfigBody)
	}
	if commit != "" {
		t.Fatalf("Fetch commit = %q, want empty: the lookup may not downgrade to plain http", commit)
	}
}

func TestFetchNoCommitResolution(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		path string
	}{
		{name: "empty ref", ref: "", path: "/u/r/HEAD/enver.yaml"},
		{name: "pinned ref", ref: "v2", path: "/u/r/v2/enver.yaml"},
		{name: "ref disagrees with the URL", ref: "HEAD", path: "/u/r/v2/enver.yaml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var lookups atomic.Int32
			mux := http.NewServeMux()
			mux.HandleFunc(tc.path, func(w http.ResponseWriter, r *http.Request) {
				_, _ = fmt.Fprint(w, testConfigBody)
			})
			mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				lookups.Add(1)
				http.NotFound(w, r)
			})
			srv := newTLSServer(t, mux)
			pointGitHubAt(t, srv.URL)
			body, commit, err := Fetch(srv.URL+tc.path, tc.ref)
			if err != nil {
				t.Fatalf("Fetch err = %v", err)
			}
			if string(body) != testConfigBody {
				t.Fatalf("Fetch body = %q, want %q", body, testConfigBody)
			}
			if commit != "" {
				t.Fatalf("Fetch commit = %q, want empty for ref %q", commit, tc.ref)
			}
			if got := lookups.Load(); got != 0 {
				t.Fatalf("Fetch made %d commit lookups for ref %q, want none", got, tc.ref)
			}
		})
	}
}

func TestFetchGistNotFoundHint(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name      string
		path      string
		status    int
		pointGist bool
		wantHint  string
	}{
		{
			name:      "a gist raw 404 names the gist form",
			path:      "/someone/abc123/raw/enver.yaml",
			status:    http.StatusNotFound,
			pointGist: true,
			wantHint:  "; the gist may name its file differently — gist:someone/abc123/<file> selects it",
		},
		{
			name:      "a pinned raw 404 already names its file",
			path:      "/someone/abc123/raw/" + revision + "/enver.yaml",
			status:    http.StatusNotFound,
			pointGist: true,
		},
		{
			name:      "another status on the gist origin stays bare",
			path:      "/someone/abc123/raw/enver.yaml",
			status:    http.StatusInternalServerError,
			pointGist: true,
		},
		{
			name:      "a 404 on another host stays bare",
			path:      "/someone/abc123/raw/enver.yaml",
			status:    http.StatusNotFound,
			pointGist: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "refused", tc.status)
			}))
			if tc.pointGist {
				pointGistAt(t, srv.URL)
			}
			_, _, err := Fetch(srv.URL+tc.path, "")
			if err == nil {
				t.Fatal("Fetch err = nil, want the failed status")
			}
			bare := fmt.Sprintf("fetch %s: %d %s", srv.URL+tc.path, tc.status, http.StatusText(tc.status))
			if tc.wantHint == "" {
				if err.Error() != bare {
					t.Fatalf("Fetch err = %v, want the bare %q", err, bare)
				}
				return
			}
			if !strings.Contains(err.Error(), tc.wantHint) {
				t.Fatalf("Fetch err = %v, want it to carry %q", err, tc.wantHint)
			}
		})
	}
}

func TestFetchHEADOutsideGitHub(t *testing.T) {
	var lookups atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/u/r/HEAD/enver.yaml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, testConfigBody)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		http.NotFound(w, r)
	})
	srv := newTLSServer(t, mux)
	pointGitHubAPIAt(t, srv.URL)
	body, commit, err := Fetch(srv.URL+"/u/r/HEAD/enver.yaml", "HEAD")
	if err != nil {
		t.Fatalf("Fetch err = %v", err)
	}
	if string(body) != testConfigBody {
		t.Fatalf("Fetch body = %q, want %q", body, testConfigBody)
	}
	if commit != "" {
		t.Fatalf("Fetch commit = %q, want empty: the host is not a GitHub raw host", commit)
	}
	if got := lookups.Load(); got != 0 {
		t.Fatalf("Fetch made %d commit lookups for a non-GitHub host, want none", got)
	}
}
