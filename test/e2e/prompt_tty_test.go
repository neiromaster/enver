//go:build unix

package e2e

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestPromptFramesStayOffStdoutRedirect pins the redirect contract end to
// end: with stdin a terminal but stdout redirected, an interactive prompt
// must render on stderr. The TUI's frames and raw-mode escape sequences may
// not land in the redirect — `enver rename p > log` leaves log empty and the
// rename itself still completes.
func TestPromptFramesStayOffStdoutRedirect(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  p:\n    env:\n      A: \"1\"\n")

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = tty.Close() }()

	redirect := filepath.Join(t.TempDir(), "stdout.log")
	out, err := os.Create(redirect)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, enverBin, "rename", "p")
	cmd.Dir = s.project
	cmd.Env = s.env
	cmd.Stdin = tty
	cmd.Stdout = out
	cmd.Stderr = tty
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Type-ahead before the child enters raw mode would ride the canonical
	// line discipline: CR translates to LF, which the prompt reads as ctrl+J,
	// not Enter. Let the TUI come up first.
	time.Sleep(500 * time.Millisecond)
	_, _ = ptmx.Write([]byte("x\r"))
	err = cmd.Wait()
	if ctx.Err() != nil {
		t.Fatal("rename prompt hung past the 10s timeout")
	}
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("rename: %v", err)
		}
		t.Fatalf("rename exited %d, want 0", ee.ExitCode())
	}

	data, err := os.ReadFile(redirect)
	if err != nil {
		t.Fatal(err)
	}
	// Plain command output (the success line) belongs on stdout; the TUI's
	// frames and raw-mode escape sequences do not.
	if strings.ContainsRune(string(data), 0x1b) || strings.ContainsRune(string(data), '\r') {
		t.Fatalf("stdout redirect captured prompt frames: %q", data)
	}
	if !strings.Contains(s.readLocal(), "x:") {
		t.Fatalf("rename did not complete: %s", s.readLocal())
	}
}
