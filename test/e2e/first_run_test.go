//go:build unix

package e2e

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestReadCommandsHintFirstRun pins the first-run refusal across the read
// paths: x reaches it through app.Run, show, export, and dotenv through
// app.ResolveProfiles, so all four name the same two working commands.
func TestReadCommandsHintFirstRun(t *testing.T) {
	s := newSandbox(t)
	for _, args := range [][]string{
		{"x", "--", "env"},
		{"show"},
		{"export"},
		{"dotenv"},
	} {
		r := s.run(args...)
		if r.ExitCode == 0 {
			t.Fatalf("enver %v must fail on an empty config, stdout: %s", args, r.Stdout)
		}
		for _, want := range []string{"no profiles defined yet", "enver add", "enver import <file> <profile>"} {
			if !strings.Contains(r.Stderr, want) {
				t.Errorf("enver %v stderr %q misses %q", args, r.Stderr, want)
			}
		}
	}
}

// TestImportThenDefaultChain pins the first-run chain: import creates the
// first profile, x without a default then names the only remaining dead end,
// and setting the default unblocks the run. No step is a dead end.
func TestImportThenDefaultChain(t *testing.T) {
	s := newSandbox(t)
	s.writeFile(filepath.Join(s.project, ".env"), "FOO=bar\n")

	if r := s.run("import", ".env", "dev"); r.ExitCode != 0 {
		t.Fatalf("import exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	r := s.run("x", "--", "env")
	if r.ExitCode == 0 {
		t.Fatal("x without a default must still fail after import")
	}
	if !strings.Contains(r.Stderr, "enver default <profile>") {
		t.Fatalf("x stderr %q must name the default command", r.Stderr)
	}
	if r := s.run("default", "dev"); r.ExitCode != 0 {
		t.Fatalf("default dev exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if r := s.run("x", "--", "env"); r.ExitCode != 0 {
		t.Fatalf("x after defaulting exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
}

// TestTypoKeepsNotFound pins the boundary: a populated config with a
// misspelled profile keeps the precise not-found error, and the first-run
// remedy never fires there.
func TestTypoKeepsNotFound(t *testing.T) {
	s := newSandbox(t)
	s.writeLocal("profiles:\n  dev:\n    env:\n      A: \"1\"\n")
	r := s.run("x", "nope", "--", "env")
	if r.ExitCode == 0 {
		t.Fatal("a misspelled profile must fail the run")
	}
	if !strings.Contains(r.Stderr, `profile "nope" not found`) {
		t.Fatalf("stderr %q must keep the not-found error", r.Stderr)
	}
	if strings.Contains(r.Stderr, "no profiles defined yet") {
		t.Fatalf("the first-run remedy must not fire on a populated config: %q", r.Stderr)
	}
}

// TestListEmptyJSONUnchanged pins the machine-readable contract: the empty
// listing stays a bare empty array, with no hint text for a consumer to parse.
func TestListEmptyJSONUnchanged(t *testing.T) {
	s := newSandbox(t)
	r := s.run("list", "--format", "json")
	if r.ExitCode != 0 {
		t.Fatalf("exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, `"profiles": []`) {
		t.Fatalf("stdout %q must stay a bare empty array", r.Stdout)
	}
	for _, banned := range []string{"no profiles defined", "enver add", "Create one at:"} {
		if strings.Contains(r.Stdout, banned) {
			t.Fatalf("the JSON listing must not carry hint text %q, got: %q", banned, r.Stdout)
		}
	}
}

// TestAddOffersDefault pins the TUI offer end to end: on an empty config add
// asks whether the first profile becomes the default, and the summary after
// confirming says so.
func TestAddOffersDefault(t *testing.T) {
	s := newSandbox(t)
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, enverBin, "add")
	cmd.Dir = s.project
	cmd.Env = s.env
	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	drained := make(chan struct{})
	go func() {
		_, _ = io.Copy(&out, ptmx)
		close(drained)
	}()

	// Type-ahead before the child enters raw mode would ride the canonical
	// line discipline: CR translates to LF, which the prompt reads as ctrl+J,
	// not Enter. Beyond that first wait, the stages must not overlap: a prompt
	// discards whatever is still queued when it exits, so each stage is
	// written only after the previous prompt had time to consume its keys and
	// hand the terminal to the next one.
	time.Sleep(500 * time.Millisecond)
	_, _ = ptmx.Write([]byte("dev\r")) // profile name
	time.Sleep(500 * time.Millisecond)
	_, _ = ptmx.Write([]byte("KEY\rval\r\r")) // env card: one entry, then submit
	time.Sleep(500 * time.Millisecond)
	_, _ = ptmx.Write([]byte("\r")) // blank name finishes the collection
	time.Sleep(500 * time.Millisecond)
	_, _ = ptmx.Write([]byte("\r")) // confirm the default prompt (Yes)

	err = cmd.Wait()
	// The drain's master read only returns once every slave reference is
	// gone: the child closed its end at exit, so release ours or the copy
	// blocks forever on macOS.
	_ = tty.Close()
	<-drained
	_ = ptmx.Close()
	if ctx.Err() != nil {
		t.Fatalf("add hung past the 10s timeout, output:\n%s", out.String())
	}
	if err != nil {
		t.Fatalf("add exited %v, output:\n%s", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, `Set "dev" as the default profile?`) {
		t.Fatalf("the default prompt must be offered, got:\n%s", got)
	}
	if !strings.Contains(got, "✓ set as default") {
		t.Fatalf("the summary must confirm the default, got:\n%s", got)
	}
	if f := s.readLocal(); !strings.Contains(f, "default: dev") {
		t.Fatalf("the default must be written, got: %s", f)
	}
}
