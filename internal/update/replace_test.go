package update

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fakeBinaryBody is the payload every platform's fakeArchive embeds.
const fakeBinaryBody = "new-enver-binary"

func TestArtifactName(t *testing.T) {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	want := fmt.Sprintf("enver_0.9.1_%s_%s.%s", runtime.GOOS, runtime.GOARCH, ext)
	if got := artifactName("v0.9.1"); got != want {
		t.Errorf("artifactName = %q, want %q", got, want)
	}
}

func TestSelfReplace(t *testing.T) {
	data := fakeArchive(t)
	sum := sha256Hex(data)
	const tag = "v1.0.0"
	archive := artifactName(tag)
	base := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/neiromaster/enver/releases/download/v1.0.0/checksums.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", sum, archive)
		case "/neiromaster/enver/releases/download/v1.0.0/" + archive:
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))

	dir := t.TempDir()
	exe := filepath.Join(dir, "enver")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := SelfReplace(NewClient(base, base), exe, tag); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != fakeBinaryBody {
		t.Errorf("replaced binary = %q, want %q", got, fakeBinaryBody)
	}
}

func TestSelfReplaceChecksumMismatch(t *testing.T) {
	data := fakeArchive(t)
	const tag = "v1.0.0"
	archive := artifactName(tag)
	base := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/neiromaster/enver/releases/download/v1.0.0/checksums.txt":
			_, _ = fmt.Fprintf(w, "deadbeef  %s\n", archive)
		case "/neiromaster/enver/releases/download/v1.0.0/" + archive:
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))

	dir := t.TempDir()
	exe := filepath.Join(dir, "enver")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := SelfReplace(NewClient(base, base), exe, tag); err == nil {
		t.Fatal("want checksum mismatch error")
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Errorf("exe changed on mismatch: %q", got)
	}
}

func TestSelfReplaceChecksumsMissingEntry(t *testing.T) {
	base := testServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/neiromaster/enver/releases/download/v1.0.0/checksums.txt" {
			_, _ = fmt.Fprint(w, "abc123  some-other-file.tar.gz\n")
			return
		}
		http.NotFound(w, r)
	}))

	dir := t.TempDir()
	exe := filepath.Join(dir, "enver")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := SelfReplace(NewClient(base, base), exe, "v1.0.0"); err == nil {
		t.Fatal("want missing-entry error")
	}
}
