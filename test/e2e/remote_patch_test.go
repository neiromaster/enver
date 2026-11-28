package e2e

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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

// patchCompletionSandbox is patchSandbox with the two names a completion list
// must treat differently from the pulled projection: a profile the local
// target owns, and a record declared but never pulled, whose absent content
// projects nothing.
func patchCompletionSandbox(t *testing.T) *sandbox {
	t.Helper()
	s := patchSandbox(t)
	s.writeLocal("remotes:\n  llm:\n    url: " + patchRemoteURL + "\n  ghost:\n    url: https://example.com/ghost.yaml\nprofiles:\n  own:\n    env:\n      A: \"1\"\n")
	return s
}

// plantPatchFile writes the --file variant beside the sandbox project: the file
// declares its own record, and the content a pull leaves for it sits in the
// store next to the file.
func (s *sandbox) plantPatchFile(t *testing.T) {
	t.Helper()
	s.writeFile(filepath.Join(s.project, "team.yaml"), "profiles:\n  t:\n    env:\n      A: \"1\"\nremotes:\n  r:\n    url: https://example.com/team.yaml\n")
	store := filepath.Join(s.project, ".enver", "team.yaml")
	s.writeFile(filepath.Join(store, "r.yaml"), "profiles:\n  default:\n    env:\n      R: \"1\"\n")
	s.writeFile(filepath.Join(store, "r.meta"), "url: https://example.com/team.yaml\npulled-at: 2026-11-15T10:00:00Z\n")
}

// plantBootstrapFile writes the --file fixture the mode-blind and refusal pins
// drive: a file declaring record llm, publish-marked when asked, and the content
// a pull leaves for it in the store beside the file.
func (s *sandbox) plantBootstrapFile(t *testing.T, publish bool) {
	t.Helper()
	body := "remotes:\n  llm:\n    url: " + patchRemoteURL + "\n"
	if publish {
		body = "publish: true\n" + body
	}
	s.writeFile(filepath.Join(s.project, "team.yaml"), body)
	store := filepath.Join(s.project, ".enver", "team.yaml")
	s.writeFile(filepath.Join(store, "llm.yaml"), patchStore)
	s.writeFile(filepath.Join(store, "llm.meta"), "url: "+patchRemoteURL+"\npulled-at: 2026-11-15T10:00:00Z\n")
}

// hasPrefixToken reports whether any candidate lies under the namespace a
// record prefixes its content with.
func hasPrefixToken(tokens []string, prefix string) bool {
	for _, token := range tokens {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
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

// exit waits for a session expected to end on a refusal rather than a clean
// finish, draining the screen so the caller can assert on what it left.
func (a *tuiSession) exit() error {
	err := a.cmd.Wait()
	_ = a.tty.Close()
	<-a.done
	return err
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

// TestEditDegradedRowDoesNotWriteNote pins the persistence half of the display
// seam: selecting the degraded inherited row keeps its card shut and reports why,
// so the note the menu shows can never be confirmed into the local copy as the
// variable's value. The save still writes the other key, and show keeps degrading
// the remote secret.
func TestEditDegradedRowDoesNotWriteNote(t *testing.T) {
	s := patchSandbox(t)
	cipher := plantForeignCiphertext(t, s)
	s.plantPatchStore(t, "profiles:\n  ollama:\n    env:\n      MODEL: glm\n      ~SECRET: "+cipher+"\n")

	e := startEdit(t, s, "llm/ollama")
	e.wait("Edit profile: llm/ollama")

	e.keys("\r") // open MODEL's inherited row
	e.wait("blank name finishes")
	e.replaceValue("llama3")
	e.wait("MODEL = llama3")

	e.keys("j") // the degraded inherited row
	e.keys("\r")
	e.wait("cannot decrypt: no key")

	e.keys("G") // jump to the last row (Done)
	e.keys("\r")
	e.wait("patches remote")
	e.finish()

	cfg := s.readLocal()
	if strings.Contains(cfg, "(cannot decrypt") {
		t.Fatalf("the degraded note must not be written as a value:\n%s", cfg)
	}
	if strings.Contains(cfg, "SECRET") {
		t.Fatalf("the degraded row must not be staged as an own key:\n%s", cfg)
	}
	if !strings.Contains(cfg, "MODEL: llama3") {
		t.Fatalf("the override of the other key must still save:\n%s", cfg)
	}

	r := s.run("show", "llm/ollama")
	if r.ExitCode != 0 {
		t.Fatalf("show: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "SECRET=(cannot decrypt: wrong key (remote))") {
		t.Fatalf("show must still degrade the remote secret:\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "MODEL=llama3") {
		t.Fatalf("the saved override must win from the local layer:\n%s", r.Stdout)
	}
}

// TestEditAddCompletionOffersRemoteNames pins discoverability: the two commands
// that bootstrap a patch copy offer the names remote content projects, beside
// the target's own names, and under --file they offer the file's own projection
// with nothing from the user layer. An unpulled record contributes no name.
func TestEditAddCompletionOffersRemoteNames(t *testing.T) {
	s := patchCompletionSandbox(t)
	for _, sub := range []string{"add", "edit"} {
		r := s.run("__complete", sub, "")
		if r.ExitCode != 0 {
			t.Fatalf("%s: exit = %d, stderr: %s", sub, r.ExitCode, r.Stderr)
		}
		tokens := completionTokens(r.Stdout)
		for _, want := range []string{"own", "llm/ollama"} {
			if !slices.Contains(tokens, want) {
				t.Errorf("%s completion = %q, want %q", sub, tokens, want)
			}
		}
		if hasPrefixToken(tokens, "ghost/") {
			t.Errorf("%s completion = %q, an unpulled record must contribute no name", sub, tokens)
		}
	}

	s.plantPatchFile(t)
	fileEdit := s.run("__complete", "edit", "--file", "team.yaml", "")
	if fileEdit.ExitCode != 0 {
		t.Fatalf("edit --file: exit = %d, stderr: %s", fileEdit.ExitCode, fileEdit.Stderr)
	}
	tokens := completionTokens(fileEdit.Stdout)
	for _, want := range []string{"t", "r/default"} {
		if !slices.Contains(tokens, want) {
			t.Fatalf("edit --file completion = %q, want %q", tokens, want)
		}
	}
	for _, gone := range []string{"own", "llm/ollama"} {
		if slices.Contains(tokens, gone) {
			t.Fatalf("edit --file completion = %q, must not carry the user layer's %q", tokens, gone)
		}
	}
}

// TestMutatorCompletionStaysTargetScoped pins the other half: every mutator that
// cannot bootstrap a patch keeps offering only the write target's own names, so
// a remote-projected or global-owned name never suggests a command the mutator
// would refuse. edit offers the remote projection but still not the global one.
func TestMutatorCompletionStaysTargetScoped(t *testing.T) {
	s := patchCompletionSandbox(t)
	s.writeGlobal("profiles:\n  g:\n    env:\n      G: \"1\"\n")

	strict := []string{"remove", "rename", "duplicate", "default", "encrypt", "decrypt"}
	for _, sub := range strict {
		r := s.run("__complete", sub, "")
		if r.ExitCode != 0 {
			t.Fatalf("%s: exit = %d, stderr: %s", sub, r.ExitCode, r.Stderr)
		}
		tokens := completionTokens(r.Stdout)
		if !slices.Contains(tokens, "own") {
			t.Errorf("%s completion = %q, want the target's own name", sub, tokens)
		}
		for _, gone := range []string{"llm/ollama", "g"} {
			if slices.Contains(tokens, gone) {
				t.Errorf("%s completion = %q, must not offer %q", sub, tokens, gone)
			}
		}
		if hasPrefixToken(tokens, "ghost/") {
			t.Errorf("%s completion = %q, an unpulled record must contribute no name", sub, tokens)
		}
	}

	edit := s.run("__complete", "edit", "")
	if edit.ExitCode != 0 {
		t.Fatalf("edit: exit = %d, stderr: %s", edit.ExitCode, edit.Stderr)
	}
	tokens := completionTokens(edit.Stdout)
	if slices.Contains(tokens, "g") {
		t.Errorf("edit completion = %q, must not offer the global-owned %q: a local-target edit would refuse it", tokens, "g")
	}
	if hasPrefixToken(tokens, "ghost/") {
		t.Errorf("edit completion = %q, an unpulled record must contribute no name", tokens)
	}
}

// TestPatchBootstrapIsModeBlind pins that the publish mark never reaches the
// writer: a publish-marked --file target offers a remote-projected name to add
// and accepts the bootstrap write exactly as an unmarked target does, and the
// file that leaves behind is what validate --strict refuses. The mark says how a
// file is judged, not what a mutator may write, so validate — not the writer —
// is the seam that reports the non-bare name the write produced.
func TestPatchBootstrapIsModeBlind(t *testing.T) {
	s := newSandbox(t)
	s.plantBootstrapFile(t, true)

	r := s.run("__complete", "add", "--file", "team.yaml", "")
	if r.ExitCode != 0 {
		t.Fatalf("completion: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if tokens := completionTokens(r.Stdout); !slices.Contains(tokens, "llm/ollama") {
		t.Fatalf("add --file completion = %q, want the published name on offer", tokens)
	}

	a := startTUI(t, s, "add", "--file", "team.yaml", "llm/ollama")
	a.wait("Extends")
	a.keys("\r") // confirm the empty extends pick
	a.wait("Variables (0 own · 2 inherited)")
	a.enter("MODEL", "llama3")
	a.wait("Variables (1 own · 1 inherited)")
	a.keys("\r") // blank name finishes the collection
	a.wait(`Set "llm/ollama" as the default profile?`)
	a.keys("n")
	a.keys("\r")
	a.wait("wrote profile")
	a.finish()

	cfg := s.readFile(filepath.Join(s.project, "team.yaml"))
	if !strings.Contains(cfg, "publish: true") {
		t.Fatalf("the mark must survive the write it did not gate:\n%s", cfg)
	}
	if !strings.Contains(cfg, "llm/ollama") || !strings.Contains(cfg, "MODEL: llama3") || strings.Contains(cfg, "TEMP") {
		t.Fatalf("the bootstrap must write a minimal patch over the published name:\n%s", cfg)
	}

	v := s.run("validate", "--strict", "team.yaml")
	if v.ExitCode != 1 {
		t.Fatalf("validate: exit = %d, stdout: %s, stderr: %s", v.ExitCode, v.Stdout, v.Stderr)
	}
	if !strings.Contains(v.Stdout, `team.yaml: error: profile "llm/ollama" is not a bare upstream profile name`) {
		t.Fatalf("validate must name the non-bare profile the blind write produced:\n%s", v.Stdout)
	}
}

// TestOtherMutatorsRefuseRemoteOnlyNames pins the refusals the bootstrap leaves
// unchanged: every mutator that cannot write the patch copy still exits non-zero
// on a remote-only name with the message its verb calls for, the --file spelling
// of the hint included, and none of them leaves the target file changed by a
// byte. Deleting intents — remove, rename, duplicate — point at the pull that
// removes the remote content; the rest point at the patch copy. Every verb is
// checked even when an earlier one fails, so one run proves all six.
func TestOtherMutatorsRefuseRemoteOnlyNames(t *testing.T) {
	s := newSandbox(t)
	s.plantBootstrapFile(t, false)
	target := filepath.Join(s.project, "team.yaml")
	before := s.readFile(target)
	const want = "enver: profile \"llm/ollama\" comes from remote \"llm\" and has no copy here; add a patch copy first (`enver add --file team.yaml llm/ollama`)\n"
	const wantPull = "enver: profile \"llm/ollama\" comes from remote \"llm\" and has no copy here; remove the remote content with `enver pull --file team.yaml --rm llm`\n"
	for _, c := range []struct {
		verb string
		args []string
		want string
	}{
		{"remove", []string{"remove", "--yes", "--file", "team.yaml", "llm/ollama"}, wantPull},
		{"rename", []string{"rename", "--file", "team.yaml", "llm/ollama", "mine"}, wantPull},
		{"duplicate", []string{"duplicate", "--file", "team.yaml", "llm/ollama", "mine"}, wantPull},
		{"default", []string{"default", "--file", "team.yaml", "llm/ollama"}, want},
		{"encrypt", []string{"encrypt", "--file", "team.yaml", "llm/ollama"}, want},
		{"decrypt", []string{"decrypt", "--file", "team.yaml", "llm/ollama"}, want},
	} {
		r := s.run(c.args...)
		if r.ExitCode != 1 {
			t.Errorf("%s: exit = %d, stderr: %s", c.verb, r.ExitCode, r.Stderr)
			continue
		}
		if r.Stderr != c.want {
			t.Errorf("%s: stderr = %q, want %q", c.verb, r.Stderr, c.want)
		}
		if after := s.readFile(target); after != before {
			t.Errorf("%s: a refused mutator must leave the target byte-identical:\n%s", c.verb, after)
		}
	}
}

// TestRemoteOnlyRefusalNamesTopLevelRecord pins that the delete refusal names a
// record a pull can remove: pull --rm takes top-level records only, so a profile
// under the nested record team/llm must be pointed at team.
func TestRemoteOnlyRefusalNamesTopLevelRecord(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("remotes:\n  team:\n    url: https://example.com/team.yaml\n")
	s.writeFile(filepath.Join(s.remotesDir(), "team.yaml"), "remotes:\n  llm:\n    url: https://example.com/team/llm.yaml\n")
	s.writeFile(filepath.Join(s.remotesDir(), "team.meta"), "url: https://example.com/team.yaml\npulled-at: 2026-11-15T10:00:00Z\n")
	s.writeFile(filepath.Join(s.remotesDir(), "team", "llm.yaml"), "profiles:\n  profile:\n    env:\n      A: \"1\"\n")
	s.writeFile(filepath.Join(s.remotesDir(), "team", "llm.meta"), "url: https://example.com/team/llm.yaml\npulled-at: 2026-11-15T10:00:00Z\n")

	r := s.run("remove", "--yes", "team/llm/profile")
	if r.ExitCode != 1 {
		t.Fatalf("exit = %d, stdout: %s, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stderr, "`enver pull --rm team`") {
		t.Fatalf("the refusal must name the top-level record a pull removes:\n%s", r.Stderr)
	}
	if strings.Contains(r.Stderr, "--rm team/llm") {
		t.Fatalf("pull --rm refuses a nested record, so the hint must not name one:\n%s", r.Stderr)
	}
}

// TestEditPatchBootstrapRefusesGlobalInvisibleRecord pins the -g guard: the
// global patch copy is read against the global file everywhere else, so the
// bootstrap may only open over a record that file declares itself. A record only
// the local file declares is refused before the menu opens, leaving both files
// untouched, while a global file declaring the same record opens the bootstrap
// exactly as a local write does.
func TestEditPatchBootstrapRefusesGlobalInvisibleRecord(t *testing.T) {
	s := patchSandbox(t)
	e := startTUI(t, s, "edit", "--global", "llm/ollama")
	waitForScreen(t, e.screen, "not declared by the global config")
	if err := e.exit(); err == nil {
		t.Fatalf("a global patch over a locally declared record must exit non-zero:\n%s", plainScreen(e.screen.String()))
	}
	screen := plainScreen(e.screen.String())
	if !strings.Contains(screen, `profile "llm/ollama" comes from remote "llm" not declared by the global config`) {
		t.Fatalf("the refusal must say the record is not the global config's:\n%s", screen)
	}
	if !strings.Contains(screen, "re-run without -g") {
		t.Fatalf("the refusal must point at dropping -g:\n%s", screen)
	}
	if cfg := s.readLocal(); strings.Contains(cfg, "llm/ollama") {
		t.Fatalf("a refused bootstrap must write nothing:\n%s", cfg)
	}

	ok := patchSandbox(t)
	ok.writeGlobal("remotes:\n  llm:\n    url: " + patchRemoteURL + "\n")
	o := startTUI(t, ok, "edit", "--global", "llm/ollama")
	o.wait("saving writes a patch copy here")
	o.keys("\x03") // ctrl+c: cancel the session without writing
	o.finish()
}
