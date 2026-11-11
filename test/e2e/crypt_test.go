package e2e

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// requireKeygen runs keygen --random and fails the test on any error.
func requireKeygen(t *testing.T, s *sandbox) {
	t.Helper()
	if r := s.run("keygen", "--random"); r.ExitCode != 0 {
		t.Fatalf("keygen --random: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
}

func TestKeygenRandomCreatesKeyFile(t *testing.T) {
	s := newSandbox(t)
	requireKeygen(t, s)
	var cache struct {
		Version int    `json:"v"`
		Salt    string `json:"salt"`
		Key     string `json:"key"`
	}
	if err := json.Unmarshal([]byte(s.readFile(s.keyPath())), &cache); err != nil {
		t.Fatalf("key cache must be JSON: %v", err)
	}
	if cache.Version != 1 || cache.Key == "" || cache.Salt == "" {
		t.Fatalf("key cache is incomplete: %+v", cache)
	}
}

func TestEncryptMarkedKey(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("profiles:\n  work:\n    env:\n      ~PROJECT_PATH: /home/anna/x\n      PLAIN: y\n")
	requireKeygen(t, s)
	if r := s.run("encrypt", "-g", "work"); r.ExitCode != 0 {
		t.Fatalf("encrypt: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	cfg := s.readFile(s.configPath())
	if !strings.Contains(cfg, "~PROJECT_PATH: enc:v3:") {
		t.Fatalf("a marked key must be encrypted under its marked spelling, got: %q", cfg)
	}
	if !strings.Contains(cfg, "PLAIN: y") {
		t.Fatalf("an unmarked non-secret key must stay plaintext, got: %q", cfg)
	}
}

func TestDecryptKeepsMarkThroughRoundTrip(t *testing.T) {
	s := newSandbox(t)
	s.writeGlobal("profiles:\n  work:\n    env:\n      ~PROJECT_PATH: /home/anna/x\n      PLAIN: y\n")
	requireKeygen(t, s)
	if r := s.run("encrypt", "-g", "work"); r.ExitCode != 0 {
		t.Fatalf("encrypt: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if cfg := s.readFile(s.configPath()); !strings.Contains(cfg, "~PROJECT_PATH: enc:v3:") {
		t.Fatalf("encrypt must produce a value for the round trip to decrypt, got: %q", cfg)
	}
	if r := s.run("decrypt", "-g", "work"); r.ExitCode != 0 {
		t.Fatalf("decrypt: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	cfg := s.readFile(s.configPath())
	if !strings.Contains(cfg, "~PROJECT_PATH: /home/anna/x") {
		t.Fatalf("decrypt must restore the value under the marked key, got: %q", cfg)
	}
	if !strings.Contains(cfg, "PLAIN: y") {
		t.Fatalf("an unmarked non-secret key must stay plaintext, got: %q", cfg)
	}
	if r := s.run("encrypt", "-g", "work"); r.ExitCode != 0 {
		t.Fatalf("second encrypt: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	cfg = s.readFile(s.configPath())
	if !strings.Contains(cfg, "~PROJECT_PATH: enc:v3:") {
		t.Fatalf("re-encrypting must pick the marked key again, got: %q", cfg)
	}
	if !strings.Contains(cfg, "PLAIN: y") {
		t.Fatalf("an unmarked non-secret key must stay plaintext, got: %q", cfg)
	}
}

func TestEncryptShowDecryptLifecycle(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      TOKEN: plainvalue1\n")
	requireKeygen(t, s)
	r := s.run("encrypt", "p")
	if r.ExitCode != 0 {
		t.Fatalf("encrypt: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(s.readLocal(), "enc:v3:") {
		t.Fatalf("encrypt must write enc:v3 values, got: %q", s.readLocal())
	}
	r = s.run("show", "--no-mask", "p")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "plainvalue1") {
		t.Fatalf("show must decrypt with the default key, got %d, %q, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
	r = s.run("decrypt", "p")
	if r.ExitCode != 0 {
		t.Fatalf("decrypt: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	local := s.readLocal()
	if strings.Contains(local, "enc:v3:") || !strings.Contains(local, "plainvalue1") {
		t.Fatalf("decrypt must restore plaintext, got: %q", local)
	}
}

// TestShowWithoutKeyDegrades pins the display/execution split: with no key
// source text show names what it cannot read and still exits 0, while JSON
// keeps failing loudly rather than handing machines ciphertext.
func TestShowWithoutKeyDegrades(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      TOKEN: plainvalue1\n")
	requireKeygen(t, s)
	if r := s.run("encrypt", "p"); r.ExitCode != 0 {
		t.Fatalf("encrypt failed: %s", r.Stderr)
	}
	if err := os.Remove(s.keyPath()); err != nil {
		t.Fatal(err)
	}
	r := s.run("show", "p")
	if r.ExitCode != 0 {
		t.Fatalf("text show must degrade, got exit %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "TOKEN=(cannot decrypt: no key — run enver keygen)") {
		t.Fatalf("the degraded row must say why, got: %q", r.Stdout)
	}
	if strings.Contains(r.Stdout, "enc:v3:") {
		t.Fatalf("ciphertext must not reach the display, got: %q", r.Stdout)
	}
	r = s.run("show", "--format", "json", "p")
	if r.ExitCode == 0 {
		t.Fatal("JSON output without a key must fail: machines would receive ciphertext")
	}
	if !strings.Contains(r.Stderr, "TOKEN") || !strings.Contains(r.Stderr, "keygen") {
		t.Fatalf("the failure should name the keys and point at recovery, got: %q", r.Stderr)
	}
}

func TestShowWithWrongKeyFails(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      TOKEN: plainvalue1\n")
	requireKeygen(t, s)
	if r := s.run("encrypt", "p"); r.ExitCode != 0 {
		t.Fatalf("encrypt failed: %s", r.Stderr)
	}
	if r := s.run("keygen", "--random", "--force"); r.ExitCode != 0 {
		t.Fatalf("key rotation failed: %s", r.Stderr)
	}
	r := s.run("show", "p")
	if r.ExitCode == 0 {
		t.Fatal("a foreign key must not decrypt the values")
	}
	if !strings.Contains(r.Stderr, "decrypt") {
		t.Fatalf("the failure should name the decrypt step, got: %q", r.Stderr)
	}
}

func TestEnverKeyEnvDecrypts(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      TOKEN: plainvalue1\n")
	requireKeygen(t, s)
	if r := s.run("encrypt", "p"); r.ExitCode != 0 {
		t.Fatalf("encrypt failed: %s", r.Stderr)
	}
	var cache struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal([]byte(s.readFile(s.keyPath())), &cache); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s.keyPath()); err != nil {
		t.Fatal(err)
	}
	s.setEnv("ENVER_KEY", cache.Key)
	r := s.run("show", "--no-mask", "p")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "plainvalue1") {
		t.Fatalf("ENVER_KEY must decrypt, got %d, %q, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}

// syncBuffer collects the pty output while the test polls it for a frame.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForScreen blocks until want has been rendered.
func waitForScreen(t *testing.T, b *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(b.String(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the TUI never showed %q; screen so far:\n%s", want, b.String())
}

// typeKeys sends one keystroke sequence and gives the TUI a moment to consume it
// before the caller waits for the next frame.
func typeKeys(t *testing.T, f *os.File, keys string) {
	t.Helper()
	if _, err := f.WriteString(keys); err != nil {
		t.Fatalf("write %q: %v", keys, err)
	}
	time.Sleep(150 * time.Millisecond)
}

// TestEditDecryptsAndReencrypts drives the interactive half of the encryption
// story through a real terminal: the menu never prints the decrypted value, the
// card offers it, and the save writes it back as ciphertext under the file's own
// key. It is the only test that exercises the card's carry of the encryption
// flag — dropping it would leave the edited text in the file in the clear.
func TestEditDecryptsAndReencrypts(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      TOKEN: plainvalue1\n")
	requireKeygen(t, s)
	if r := s.run("encrypt", "p"); r.ExitCode != 0 {
		t.Fatalf("encrypt: exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(s.readLocal(), "enc:v3:") {
		t.Fatalf("setup: the value is not sealed: %s", s.readLocal())
	}

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
	cmd := exec.CommandContext(ctx, enverBin, "edit", "p")
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

	waitForScreen(t, screen, "Edit profile: p")
	if strings.Contains(screen.String(), "plainvalue1") {
		t.Fatalf("the menu printed the decrypted value:\n%s", screen.String())
	}

	typeKeys(t, ptmx, "\r") // open TOKEN's card
	waitForScreen(t, screen, "plainvalue1")
	typeKeys(t, ptmx, "\t") // name → value
	typeKeys(t, ptmx, "Z")  // append to the decrypted text
	typeKeys(t, ptmx, "\t") // value → comment
	typeKeys(t, ptmx, "\r") // submit the card
	waitForScreen(t, screen, "unsaved changes")
	typeKeys(t, ptmx, "G") // jump to the last row (Done)
	typeKeys(t, ptmx, "\r")

	if err := cmd.Wait(); err != nil || ctx.Err() != nil {
		t.Fatalf("edit did not finish cleanly: err=%v ctx=%v\n%s", err, ctx.Err(), screen.String())
	}
	// The master read only ends once this side drops its own handle on the
	// slave: the child is gone, but the pty still has a live writer until then.
	_ = tty.Close()
	<-done

	cfg := s.readLocal()
	if !strings.Contains(cfg, "enc:v3:") {
		t.Fatalf("the saved value is not ciphertext: %s", cfg)
	}
	if strings.Contains(cfg, "plainvalue1Z") {
		t.Fatalf("the edited plaintext reached the file: %s", cfg)
	}
	r := s.run("show", "--no-mask", "p")
	if r.ExitCode != 0 || !strings.Contains(r.Stdout, "TOKEN=plainvalue1Z") {
		t.Fatalf("the sealed value does not read back: exit %d, %q, stderr: %s", r.ExitCode, r.Stdout, r.Stderr)
	}
}
