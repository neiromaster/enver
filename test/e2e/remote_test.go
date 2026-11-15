package e2e

import (
	"context"
	"encoding/pem"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// remoteBody is the config the fake repository publishes: a provider profile and
// a model profile extending it from the same file, which is the shape a pull
// projects into the record's namespace.
const remoteBody = `profiles:
  ollama:
    env:
      OLLAMA_HOST: http://localhost:11434
      OLLAMA_MODEL_SERIES: v0.5
  qwen:
    extends: ollama
    env:
      MODEL: qwen2.5-coder:32b
`

// pulledPatch is the user layer's patch of a projected profile: one secret key,
// which composes with everything the remote supplies.
const pulledPatch = `profiles:
  llm/ollama:
    env:
      OLLAMA_API_KEY: sk-live
`

// serveRepo starts a fake repository over TLS and makes the sandbox trust its
// certificate. enver fetches https only, so the certificate is trusted rather
// than bypassed: Go reads SSL_CERT_FILE for that on Unix.
func serveRepo(t *testing.T, s *sandbox, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/llm.yaml" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	certPath := filepath.Join(t.TempDir(), "repo.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(certPath, cert, 0o600); err != nil {
		t.Fatal(err)
	}
	// The developer's own SSL_CERT_FILE would otherwise shadow the sandbox's, so
	// the trust anchor is installed rather than appended.
	s.dropEnv("SSL_CERT_FILE", "SSL_CERT_DIR")
	s.setEnv("SSL_CERT_FILE", certPath)
	return srv
}

// remotesDir is where pulled content and its provenance live.
func (s *sandbox) remotesDir() string {
	return filepath.Join(s.home, ".config", "enver", "remotes")
}

// seedPulledRemote writes what a pull leaves on disk: the pristine content, the
// sidecar recording where it came from, and the record declaring it.
func (s *sandbox) seedPulledRemote(name, url, body string) {
	s.t.Helper()
	s.writeFile(filepath.Join(s.remotesDir(), name+".yaml"), body)
	s.writeFile(filepath.Join(s.remotesDir(), name+".meta"), "url: "+url+"\npulled-at: 2026-11-15T10:00:00Z\n")
	s.writeGlobal("remotes:\n  " + name + ":\n    url: " + url + "\n")
}

// TestE2ERemotePull drives the remote story through the binary: register a
// source, patch a projected profile, run the composed env, re-pull, notice a
// record that moved away from its content, and remove the record. Registration
// and re-fetch need a real fetch, and enver fetches https only — the fake
// repository's certificate is trusted through SSL_CERT_FILE, which Go reads on
// Unix and not on Windows. Windows therefore seeds the artifacts a pull leaves
// behind and skips the fetch-only assertions; every step after the pull still
// runs there.
func TestE2ERemotePull(t *testing.T) {
	s := newSandbox(t)
	liveFetch := runtime.GOOS != "windows"

	var source string
	if liveFetch {
		srv := serveRepo(t, s, remoteBody)
		source = srv.URL + "/llm.yaml"
		r := s.run("pull", source, "--as", "llm")
		if r.ExitCode != 0 {
			t.Fatalf("pull: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
		}
		for _, want := range []string{`remote "llm"`, "llm/ollama", "llm/qwen"} {
			if !strings.Contains(r.Stdout, want) {
				t.Fatalf("pull summary misses %q:\n%s", want, r.Stdout)
			}
		}
		if rec := s.readFile(s.configPath()); !strings.Contains(rec, "remotes:") || !strings.Contains(rec, "url: "+source) {
			t.Fatalf("registration must write the record to the global config, got:\n%s", rec)
		}
		// Stored content is the pristine upstream copy: what the user diffs
		// against the URL has to be exactly what the repo publishes.
		if got := s.readFile(filepath.Join(s.remotesDir(), "llm.yaml")); got != remoteBody {
			t.Fatalf("stored content is not the fetched bytes:\n%q", got)
		}
	} else {
		source = "https://example.com/llm.yaml"
		s.seedPulledRemote("llm", source, remoteBody)
	}

	// The project's local file patches the projected provider profile.
	s.writeLocal(pulledPatch)

	// Composed: the remote supplies the endpoint and the series, the patch
	// supplies the key, and the model profile inherits all of it.
	composed := map[string]string{
		"MODEL":               "qwen2.5-coder:32b",
		"OLLAMA_HOST":         "http://localhost:11434",
		"OLLAMA_MODEL_SERIES": "v0.5",
		"OLLAMA_API_KEY":      "sk-live",
	}
	for _, key := range slices.Sorted(maps.Keys(composed)) {
		r := s.run(append([]string{"x", "llm/qwen", "--"}, childPrinter(key)...)...)
		if r.ExitCode != 0 {
			t.Fatalf("x llm/qwen: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
		}
		if got := strings.TrimSpace(r.Stdout); got != composed[key] {
			t.Fatalf("child %s = %q, want %q (stderr: %s)", key, got, composed[key], r.Stderr)
		}
	}

	// Provenance is what lets a user notice a swapped endpoint: show names the
	// remote layer for the inherited keys and the local layer for the patch.
	shown := s.run("show", "--no-mask", "llm/qwen")
	if shown.ExitCode != 0 {
		t.Fatalf("show: exit = %d, stderr: %s", shown.ExitCode, shown.Stderr)
	}
	for _, want := range []string{"OLLAMA_HOST=http://localhost:11434  # from llm/ollama (remote:llm)", "OLLAMA_API_KEY=sk-live  # from llm/ollama (local)"} {
		if !strings.Contains(shown.Stdout, want) {
			t.Fatalf("show misses %q:\n%s", want, shown.Stdout)
		}
	}

	if liveFetch {
		r := s.run("pull", "llm")
		if r.ExitCode != 0 {
			t.Fatalf("re-pull: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
		}
		if got := s.readFile(s.localPath()); got != pulledPatch {
			t.Fatalf("a re-pull must leave the user layer alone, got:\n%s", got)
		}
		again := s.run(append([]string{"x", "llm/qwen", "--"}, childPrinter("OLLAMA_API_KEY")...)...)
		if again.ExitCode != 0 || strings.TrimSpace(again.Stdout) != "sk-live" {
			t.Fatalf("the patch must survive a re-pull, got %d, %q, stderr: %s", again.ExitCode, again.Stdout, again.Stderr)
		}
	}

	// A record re-pointed by hand serves the stored content offline and says so
	// on stderr: silence here is the failure the sidecar exists to prevent.
	s.writeGlobal("remotes:\n  llm:\n    url: https://example.com/moved.yaml\n")
	diverged := s.run("show", "llm/ollama")
	if diverged.ExitCode != 0 {
		t.Fatalf("show: exit = %d, stderr: %s", diverged.ExitCode, diverged.Stderr)
	}
	for _, want := range []string{`warning: remote "llm"`, "content was fetched from " + source, "https://example.com/moved.yaml"} {
		if !strings.Contains(diverged.Stderr, want) {
			t.Fatalf("stderr misses the divergence warning %q:\n%s", want, diverged.Stderr)
		}
	}

	// Removal deletes the record and its content, and reports the patch profile
	// that now stands alone.
	rm := s.run("pull", "--rm", "llm")
	if rm.ExitCode != 0 {
		t.Fatalf("pull --rm: exit = %d, stderr: %s", rm.ExitCode, rm.Stderr)
	}
	if !strings.Contains(rm.Stdout, `remote "llm"`) || !strings.Contains(rm.Stdout, "no longer inherits") {
		t.Fatalf("removal must report the orphaned patch:\n%s", rm.Stdout)
	}
	if got := s.readFile(s.configPath()); strings.Contains(got, "moved.yaml") {
		t.Fatalf("the record survived removal:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(s.remotesDir(), "llm.yaml")); !os.IsNotExist(err) {
		t.Fatalf("the last declaration must delete the stored content, stat err: %v", err)
	}
	if got := s.readLocal(); got != pulledPatch {
		t.Fatalf("removal must leave the patch profile in place, got:\n%s", got)
	}
	alone := s.run("show", "llm/ollama")
	if alone.ExitCode != 0 {
		t.Fatalf("show the orphaned patch: exit = %d, stderr: %s", alone.ExitCode, alone.Stderr)
	}
	if strings.Contains(alone.Stdout, "OLLAMA_HOST") || !strings.Contains(alone.Stdout, "OLLAMA_API_KEY") {
		t.Fatalf("the orphaned patch must keep only its own keys, got:\n%s", alone.Stdout)
	}
	if gone := s.run("show", "llm/qwen"); gone.ExitCode == 0 {
		t.Fatalf("a remote-only profile must be gone with its content, got:\n%s", gone.Stdout)
	}
}

// TestE2ERemoteLoadWarningReachesTheTUI pins the load path no read-command test
// reaches: an interactive command reports the advisories a merged load collects
// on stderr before it starts drawing, so a divergent remote is not silent just
// because the command driving the load is `add`.
func TestE2ERemoteLoadWarningReachesTheTUI(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("remotes:\n  llm:\n    url: https://example.com/new.yaml\nprofiles:\n  dev:\n    env:\n      A: \"1\"\n")
	s.writeFile(filepath.Join(s.remotesDir(), "llm.yaml"), "profiles:\n  base:\n    env:\n      K: pulled\n")
	s.writeFile(filepath.Join(s.remotesDir(), "llm.meta"), "url: https://example.com/old.yaml\npulled-at: 2026-11-15T10:00:00Z\n")

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = tty.Close() }()
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Skipf("cannot size the pty: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, enverBin, "add", "llm/patch")
	cmd.Dir = s.project
	cmd.Env = s.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	screen := &syncBuffer{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(screen, ptmx)
	}()

	waitForScreen(t, screen, `warning: remote "llm": content was fetched from https://example.com/old.yaml`)
	// Wait for the picker before cancelling: a ctrl+c sent before the terminal is
	// in raw mode is a signal, not a keystroke.
	waitForScreen(t, screen, "Extends")
	typeKeys(t, ptmx, "\x03")

	if err := cmd.Wait(); err != nil || ctx.Err() != nil {
		t.Fatalf("add did not finish cleanly: err=%v ctx=%v\n%s", err, ctx.Err(), screen.String())
	}
	_ = tty.Close()
	<-done

	if _, err := os.Stat(s.localPath()); !os.IsNotExist(err) {
		t.Fatalf("a cancelled add must not write the local layer, stat err: %v", err)
	}
}
