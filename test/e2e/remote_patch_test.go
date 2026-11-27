package e2e

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

const patchRemoteURL = "https://example.com/llm.yaml"

// patchStore is the pristine content a pull leaves behind for the llm record:
// the projected profile ollama, with two plain keys the bootstrap backdrop
// should surface.
const patchStore = `profiles:
  ollama:
    env:
      MODEL: glm
      TEMP: "0.7"
`

// patchSandbox is the bootstrap fixture: a local record declaring llm, and the
// store a pull leaves behind. The record lives in the local file the add writes
// to, so the layer under test carries both its own record and the new profile.
func patchSandbox(t *testing.T) *sandbox {
	t.Helper()
	s := newSandbox(t)
	s.writeLocal("remotes:\n  llm:\n    url: " + patchRemoteURL + "\n")
	s.plantPatchStore(t, patchStore)
	return s
}

// plantPatchStore writes remote content and the state sidecar a pull records,
// so a load sees declared content that does not diverge from its record.
func (s *sandbox) plantPatchStore(t *testing.T, body string) {
	t.Helper()
	s.writeFile(filepath.Join(s.remotesDir(), "llm.yaml"), body)
	s.writeFile(filepath.Join(s.remotesDir(), "llm.meta"), "url: "+patchRemoteURL+"\npulled-at: 2026-11-15T10:00:00Z\n")
}

// addSession drives one interactive add process over a pty.
type addSession struct {
	t      *testing.T
	ptmx   *os.File
	tty    *os.File
	cmd    *exec.Cmd
	screen *syncBuffer
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

func startAdd(t *testing.T, s *sandbox, name string) *addSession {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		_ = ptmx.Close()
		_ = tty.Close()
		t.Skipf("cannot size the pty: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, enverBin, "add", name)
	cmd.Dir = s.project
	cmd.Env = s.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	if err := cmd.Start(); err != nil {
		cancel()
		_ = ptmx.Close()
		_ = tty.Close()
		t.Fatal(err)
	}
	a := &addSession{t: t, ptmx: ptmx, tty: tty, cmd: cmd, screen: &syncBuffer{}, done: make(chan struct{}), ctx: ctx, cancel: cancel}
	go func() {
		defer close(a.done)
		_, _ = io.Copy(a.screen, ptmx)
	}()
	t.Cleanup(func() {
		cancel()
		_ = ptmx.Close()
		_ = tty.Close()
		_ = cmd.Wait()
		<-a.done
	})
	return a
}

func (a *addSession) wait(want string) {
	a.t.Helper()
	waitForScreen(a.t, a.screen, want)
}

func (a *addSession) keys(keys string) {
	a.t.Helper()
	typeKeys(a.t, a.ptmx, keys)
}

// enter types one env card and submits it.
func (a *addSession) enter(name, value string) {
	a.t.Helper()
	for _, c := range name {
		a.keys(string(c))
	}
	a.keys("\t")
	for _, c := range value {
		a.keys(string(c))
	}
	a.keys("\t")
	a.keys("\r")
}

// finish waits for the process, closes the slave, and drains the screen. It
// fails the test unless the command exited cleanly within the timeout.
func (a *addSession) finish() string {
	a.t.Helper()
	err := a.cmd.Wait()
	_ = a.tty.Close()
	<-a.done
	if err != nil || a.ctx.Err() != nil {
		a.t.Fatalf("add did not finish cleanly: err=%v ctx=%v\n%s", err, a.ctx.Err(), a.screen.String())
	}
	return a.screen.String()
}

var ansiSeqRe = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// plainScreen drops the SGR and cursor sequences so row assertions can span the
// styled icon and its label.
func plainScreen(screen string) string {
	return ansiSeqRe.ReplaceAllString(screen, "")
}

// TestAddPatchBackdropShowsRemoteKeys drives add on a name only remote content
// provides: the collector opens on the remote keys, the typed key is marked as an
// override of one, and the save writes only what the user entered.
func TestAddPatchBackdropShowsRemoteKeys(t *testing.T) {
	s := patchSandbox(t)
	a := startAdd(t, s, "llm/ollama")

	a.wait("Extends")
	a.keys("\r") // confirm the empty extends pick
	a.wait("Variables (0 own · 2 inherited)")
	first := plainScreen(a.screen.String())
	for _, want := range []string{"↳ MODEL = glm", "↳ TEMP = 0.7"} {
		if !strings.Contains(first, want) {
			t.Fatalf("the first frame must show %q as inherited:\n%s", want, first)
		}
	}

	a.enter("MODEL", "llama3")
	a.wait("Variables (1 own · 1 inherited)")
	frame := plainScreen(a.screen.String())
	if !strings.Contains(frame, "↻ MODEL = llama3") {
		t.Fatalf("the typed row must be marked as an override of the inherited key:\n%s", frame)
	}

	a.keys("\r") // blank name finishes the collection
	a.wait(`Set "llm/ollama" as the default profile?`)
	a.keys("n")
	a.keys("\r")
	a.wait("wrote profile")
	a.finish()

	cfg := s.readLocal()
	if !strings.Contains(cfg, "llm/ollama") || !strings.Contains(cfg, "MODEL: llama3") {
		t.Fatalf("the patch copy must carry the typed entry:\n%s", cfg)
	}
	if strings.Contains(cfg, "TEMP") {
		t.Fatalf("the backdrop must not be written into the patch copy:\n%s", cfg)
	}

	r := s.run("show", "llm/ollama")
	if r.ExitCode != 0 {
		t.Fatalf("show: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "MODEL=llama3") || !strings.Contains(r.Stdout, "# from llm/ollama (local)") {
		t.Fatalf("the typed key must win from the local layer:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "TEMP=0.7") || !strings.Contains(r.Stdout, "# from llm/ollama (remote:llm)") {
		t.Fatalf("the untouched key must still come from the remote:\n%s", r.Stdout)
	}
}

// TestAddPatchBackdropFoldsMarks pins the mark the remote supplied: overriding a
// remote-marked key in plaintext keeps the key marked, so the saved copy stays
// masked and encrypt-eligible.
func TestAddPatchBackdropFoldsMarks(t *testing.T) {
	s := patchSandbox(t)
	s.plantPatchStore(t, `profiles:
  ollama:
    env:
      ~TOKEN: abc
      MODEL: glm
`)
	requireKeygen(t, s)
	a := startAdd(t, s, "llm/ollama")

	a.wait("Extends")
	a.keys("\r")
	a.wait("Variables (0 own · 2 inherited)")
	a.enter("TOKEN", "fresh-token")
	a.wait("Variables (1 own · 1 inherited)")
	a.keys("\r")
	a.wait(`Set "llm/ollama" as the default profile?`)
	a.keys("n")
	a.keys("\r")
	a.wait("wrote profile")
	a.finish()

	cfg := s.readLocal()
	if !strings.Contains(cfg, "~TOKEN: fresh-token") {
		t.Fatalf("the written key must keep the remote mark (loader-folded spelling):\n%s", cfg)
	}

	r := s.run("show", "llm/ollama")
	if r.ExitCode != 0 {
		t.Fatalf("show: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if strings.Contains(r.Stdout, "fresh-token") {
		t.Fatalf("a marked key must be masked by show:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "TOKEN=") || !strings.Contains(r.Stdout, "marked, not encrypted") {
		t.Fatalf("show must mask the marked row and say it is not encrypted yet:\n%s", r.Stdout)
	}

	if r := s.run("encrypt", "llm/ollama"); r.ExitCode != 0 {
		t.Fatalf("encrypt: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if cfg := s.readLocal(); !strings.Contains(cfg, "~TOKEN: enc:v3:") {
		t.Fatalf("the inherited mark must make encrypt seal the value:\n%s", cfg)
	}
}

// sealedExport extracts the ciphertext encrypt wrote for key from a config body.
func sealedExport(t *testing.T, cfg, key string) string {
	t.Helper()
	for _, line := range strings.Split(cfg, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "~"+key+": "); ok {
			if !strings.HasPrefix(v, "enc:v3:") {
				t.Fatalf("%s is not ciphertext: %q", key, v)
			}
			return v
		}
	}
	t.Fatalf("no sealed %s in:\n%s", key, cfg)
	return ""
}

// TestAddPatchBackdropDegradesUndecryptable pins the display seam: remote
// ciphertext the configured key cannot read degrades to a per-key note instead of
// failing the collector, and the user can still save a patch of another key.
func TestAddPatchBackdropDegradesUndecryptable(t *testing.T) {
	s := patchSandbox(t)

	// Author ciphertext under a key that goes away before the add runs.
	s.writeLocal("remotes:\n  llm:\n    url: " + patchRemoteURL + "\nprofiles:\n  foreign:\n    env:\n      ~SECRET: planted-secret\n")
	requireKeygen(t, s)
	if r := s.run("encrypt", "foreign"); r.ExitCode != 0 {
		t.Fatalf("encrypt foreign: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	cipher := sealedExport(t, s.readLocal(), "SECRET")
	if r := s.run("keygen", "--random", "--force"); r.ExitCode != 0 {
		t.Fatalf("key rotation: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	s.writeLocal("remotes:\n  llm:\n    url: " + patchRemoteURL + "\n")
	s.plantPatchStore(t, "profiles:\n  ollama:\n    env:\n      MODEL: glm\n      SECRET: "+cipher+"\n")

	a := startAdd(t, s, "llm/ollama")
	a.wait("Extends")
	a.keys("\r")
	a.wait("Variables (0 own · 2 inherited)")
	frame := plainScreen(a.screen.String())
	if !strings.Contains(frame, "↳ SECRET = (cannot decrypt: wrong key (remote))") {
		t.Fatalf("the unreadable remote key must degrade to a note in the frame:\n%s", frame)
	}
	if !strings.Contains(frame, "note: SECRET cannot be decrypted from the remote") {
		t.Fatalf("the per-key warning must reach the terminal:\n%s", frame)
	}

	a.enter("MODEL", "llama3")
	a.wait("Variables (1 own · 1 inherited)")
	a.keys("\r")
	a.wait(`Set "llm/ollama" as the default profile?`)
	a.keys("n")
	a.keys("\r")
	a.wait("wrote profile")
	a.finish()

	cfg := s.readLocal()
	if !strings.Contains(cfg, "MODEL: llama3") || strings.Contains(cfg, "SECRET") {
		t.Fatalf("overriding a different key must save only that key:\n%s", cfg)
	}
}
