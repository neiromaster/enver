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

// tuiSession drives one interactive enver process over a pty.
type tuiSession struct {
	t      *testing.T
	ptmx   *os.File
	tty    *os.File
	cmd    *exec.Cmd
	screen *syncBuffer
	done   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
}

// startTUI runs the built binary with args on a sized pty, answering the
// terminal questions through keys.
func startTUI(t *testing.T, s *sandbox, args ...string) *tuiSession {
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
	cmd := exec.CommandContext(ctx, enverBin, args...)
	cmd.Dir = s.project
	cmd.Env = s.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	if err := cmd.Start(); err != nil {
		cancel()
		_ = ptmx.Close()
		_ = tty.Close()
		t.Fatal(err)
	}
	a := &tuiSession{t: t, ptmx: ptmx, tty: tty, cmd: cmd, screen: &syncBuffer{}, done: make(chan struct{}), ctx: ctx, cancel: cancel}
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

func startAdd(t *testing.T, s *sandbox, name string) *tuiSession {
	t.Helper()
	return startTUI(t, s, "add", name)
}

func startEdit(t *testing.T, s *sandbox, name string) *tuiSession {
	t.Helper()
	return startTUI(t, s, "edit", name)
}

func (a *tuiSession) wait(want string) {
	a.t.Helper()
	waitForScreen(a.t, a.screen, want)
}

func (a *tuiSession) keys(keys string) {
	a.t.Helper()
	typeKeys(a.t, a.ptmx, keys)
}

// enter types one env card and submits it.
func (a *tuiSession) enter(name, value string) {
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

// replaceValue clears the value field a card seeded, types value, and submits.
// The card opens on the name field; the cursor rides at the end of the seed.
func (a *tuiSession) replaceValue(value string) {
	a.t.Helper()
	a.keys("\t")
	a.keys("\x15") // ctrl+u: drop the seeded value
	for _, c := range value {
		a.keys(string(c))
	}
	a.keys("\t")
	a.keys("\r")
}

// finish waits for the process, closes the slave, and drains the screen. It
// fails the test unless the command exited cleanly within the timeout.
func (a *tuiSession) finish() string {
	a.t.Helper()
	err := a.cmd.Wait()
	_ = a.tty.Close()
	<-a.done
	if err != nil || a.ctx.Err() != nil {
		a.t.Fatalf("the session did not finish cleanly: err=%v ctx=%v\n%s", err, a.ctx.Err(), a.screen.String())
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
	if strings.Contains(cfg, "MODEL") {
		t.Fatalf("the untouched remote key must not be written into the patch copy:\n%s", cfg)
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

// plantForeignCiphertext authors a sealed value under a key that goes away, so
// the sandbox key cannot read the ciphertext it returns: the remote-ciphertext
// fixture both bootstrap-degrade tests share.
func plantForeignCiphertext(t *testing.T, s *sandbox) string {
	t.Helper()
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
	return cipher
}

// TestAddPatchBackdropDegradesUndecryptable pins the display seam: remote
// ciphertext the configured key cannot read degrades to a per-key note instead of
// failing the collector, and the user can still save a patch of another key.
func TestAddPatchBackdropDegradesUndecryptable(t *testing.T) {
	s := patchSandbox(t)
	cipher := plantForeignCiphertext(t, s)
	// Sealed remote values always carry the mark, so the fixture does too: the
	// note is visible only because the backdrop drops the mark before display,
	// and keeping the mark would mask the note a reader needs.
	s.plantPatchStore(t, "profiles:\n  ollama:\n    env:\n      MODEL: glm\n      ~SECRET: "+cipher+"\n")

	a := startAdd(t, s, "llm/ollama")
	a.wait("Extends")
	a.keys("\r")
	a.wait("Variables (0 own · 2 inherited)")
	frame := plainScreen(a.screen.String())
	if !strings.Contains(frame, "↳ SECRET = (cannot decrypt: wrong key (remote))") {
		t.Fatalf("the unreadable remote key must degrade to an unmasked note in the frame:\n%s", frame)
	}
	if strings.Contains(frame, "~SECRET") || strings.Contains(frame, "(can…") {
		t.Fatalf("the degraded row must not render under the remote mark:\n%s", frame)
	}
	if got := strings.Count(frame, "note: SECRET cannot be decrypted from the remote"); got != 1 {
		t.Fatalf("the per-key warning must print exactly once, got %d:\n%s", got, frame)
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

// TestEditPatchBootstrapOpensOnRemoteBase drives edit on a name only remote
// content provides: the menu opens on the remote keys as inherited rows, the
// notice names the remote whose content the save patches, overriding one key
// marks it as an override, and the written copy carries only what the user
// entered.
func TestEditPatchBootstrapOpensOnRemoteBase(t *testing.T) {
	s := patchSandbox(t)
	e := startEdit(t, s, "llm/ollama")

	e.wait("Edit profile: llm/ollama")
	first := plainScreen(e.screen.String())
	for _, want := range []string{"↳  MODEL = glm", "↳  TEMP = 0.7"} {
		if !strings.Contains(first, want) {
			t.Fatalf("the first frame must list %q as inherited:\n%s", want, first)
		}
	}
	for _, want := range []string{`profile "llm/ollama" comes from remote "llm"`, "saving writes a patch copy here"} {
		if !strings.Contains(first, want) {
			t.Fatalf("the bootstrap notice must say %q:\n%s", want, first)
		}
	}

	e.keys("\r") // open MODEL's inherited row
	e.wait("blank name finishes")
	e.replaceValue("llama3")
	e.wait("MODEL = llama3")
	frame := plainScreen(e.screen.String())
	if !strings.Contains(frame, "↻  MODEL = llama3") {
		t.Fatalf("the overridden row must be marked as an override of the remote key:\n%s", frame)
	}
	if !strings.Contains(frame, "unsaved changes") {
		t.Fatalf("the override must read as an unsaved change:\n%s", frame)
	}

	e.keys("G") // jump to the last row (Done)
	e.keys("\r")
	e.wait("patches remote")
	screen := e.finish()

	if !strings.Contains(screen, `patches remote "llm"`) {
		t.Fatalf("the closing report must name the patched remote:\n%s", screen)
	}
	cfg := s.readLocal()
	if !strings.Contains(cfg, "MODEL: llama3") || strings.Contains(cfg, "TEMP") {
		t.Fatalf("the patch copy must carry only the overridden key:\n%s", cfg)
	}
	if strings.Contains(cfg, "extends") {
		t.Fatalf("the patch copy must not grow a chain of its own:\n%s", cfg)
	}

	r := s.run("show", "llm/ollama")
	if r.ExitCode != 0 {
		t.Fatalf("show: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "MODEL=llama3") || !strings.Contains(r.Stdout, "# from llm/ollama (local)") {
		t.Fatalf("the overridden key must win from the local layer:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "TEMP=0.7") || !strings.Contains(r.Stdout, "# from llm/ollama (remote:llm)") {
		t.Fatalf("the untouched key must still come from the remote:\n%s", r.Stdout)
	}
}

// TestEditPatchBootstrapThenEditIsNormal pins the notice as one-shot: once the
// copy exists, edit opens on it as a plain session, listing the copy's own rows
// and reporting no patched remote.
func TestEditPatchBootstrapThenEditIsNormal(t *testing.T) {
	s := patchSandbox(t)
	s.writeLocal("remotes:\n  llm:\n    url: " + patchRemoteURL + "\nprofiles:\n  llm/ollama:\n    env:\n      MODEL: llama3\n")
	e := startEdit(t, s, "llm/ollama")

	e.wait("Edit profile: llm/ollama")
	frame := plainScreen(e.screen.String())
	for _, gone := range []string{"comes from remote", "saving writes a patch copy"} {
		if strings.Contains(frame, gone) {
			t.Fatalf("an existing copy must not announce a bootstrap (%q):\n%s", gone, frame)
		}
	}
	if strings.Contains(frame, "↳  MODEL") || !strings.Contains(frame, "MODEL = llama3") {
		t.Fatalf("MODEL must be an own row, not an inherited one:\n%s", frame)
	}

	e.keys("G") // jump to the last row (Done)
	e.keys("\r")
	e.wait("updated profile")
	screen := e.finish()

	if strings.Contains(screen, "patches remote") {
		t.Fatalf("a plain edit must not report a patched remote:\n%s", screen)
	}
	if cfg := s.readLocal(); !strings.Contains(cfg, "MODEL: llama3") {
		t.Fatalf("the copy must survive the no-op edit:\n%s", cfg)
	}
}

// TestEditPatchBootstrapDegradesUndecryptable pins the display seam in edit:
// remote ciphertext the configured key cannot read degrades to the note the menu
// can show instead of failing the session, and the save still writes the patch of
// another key alone.
func TestEditPatchBootstrapDegradesUndecryptable(t *testing.T) {
	s := patchSandbox(t)
	cipher := plantForeignCiphertext(t, s)
	s.plantPatchStore(t, "profiles:\n  ollama:\n    env:\n      MODEL: glm\n      ~SECRET: "+cipher+"\n")

	e := startEdit(t, s, "llm/ollama")
	e.wait("Edit profile: llm/ollama")
	frame := plainScreen(e.screen.String())
	if !strings.Contains(frame, "↳  SECRET = (cannot decrypt: wrong key (remote))") {
		t.Fatalf("the unreadable remote key must degrade to an unmasked note in the frame:\n%s", frame)
	}
	if strings.Contains(frame, "~SECRET") {
		t.Fatalf("the degraded row must not render under the remote mark:\n%s", frame)
	}
	if got := strings.Count(frame, "note: SECRET cannot be decrypted from the remote"); got != 1 {
		t.Fatalf("the per-key warning must print exactly once, got %d:\n%s", got, frame)
	}

	e.keys("\r") // open MODEL's inherited row
	e.wait("blank name finishes")
	e.replaceValue("llama3")
	e.wait("MODEL = llama3")
	e.keys("G") // jump to the last row (Done)
	e.keys("\r")
	e.wait("patches remote")
	e.finish()

	cfg := s.readLocal()
	if !strings.Contains(cfg, "MODEL: llama3") || strings.Contains(cfg, "SECRET") {
		t.Fatalf("overriding a different key must save only that key:\n%s", cfg)
	}
}
