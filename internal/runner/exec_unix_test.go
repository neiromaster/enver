//go:build unix

package runner

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeProbe(t *testing.T, path string, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatal(err)
	}
}

func TestLookPathIn(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	exe1 := filepath.Join(dir1, "probe-cmd")
	exe2 := filepath.Join(dir2, "probe-cmd")
	writeProbe(t, exe1, 0o755)
	writeProbe(t, exe2, 0o755)
	noexec := filepath.Join(dir1, "noexec-cmd")
	writeProbe(t, noexec, 0o644)
	if err := os.Mkdir(filepath.Join(dir1, "dir-cmd"), 0o755); err != nil {
		t.Fatal(err)
	}

	sep := string(os.PathListSeparator)
	cases := []struct {
		name    string
		file    string
		path    string
		want    string
		wantErr error
	}{
		{"absolute dir", "probe-cmd", dir1, exe1, nil},
		{"first entry wins", "probe-cmd", dir2 + sep + dir1, exe2, nil},
		{"non-executable skipped", "noexec-cmd", dir1, "", exec.ErrNotFound},
		{"directory skipped", "dir-cmd", dir1, "", exec.ErrNotFound},
		{"missing everywhere", "ghost-cmd", dir1, "", exec.ErrNotFound},
		{"empty path list finds nothing", "probe-cmd", "", "", exec.ErrNotFound},
		{"separator name resolves directly", exe1, dir2, exe1, nil},
		{"separator name missing reports the stat error", filepath.Join(dir1, "missing-cmd"), dir1, "", fs.ErrNotExist},
		{"separator name without x-bit reports EACCES", noexec, dir1, "", fs.ErrPermission},
	}
	for _, c := range cases {
		got, err := lookPathIn(c.file, c.path)
		if c.wantErr != nil {
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("%s: err = %v, want %v", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s = %q, want %q", c.name, got, c.want)
		}
	}

	t.Run("relative resolution yields ErrDot", func(t *testing.T) {
		t.Chdir(dir1)
		for _, path := range []string{".", sep} {
			got, err := lookPathIn("probe-cmd", path)
			if !errors.Is(err, exec.ErrDot) {
				t.Fatalf("path %q: err = %v, want ErrDot", path, err)
			}
			if got != "probe-cmd" {
				t.Fatalf("path %q = %q, want the relative probe-cmd", path, got)
			}
		}
	})
}

func TestRunLookupErrorSurfacesVerbatim(t *testing.T) {
	noexec := filepath.Join(t.TempDir(), "nope")
	writeProbe(t, noexec, 0o644)

	pipeR, pipeW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	os.Stderr = pipeW
	t.Cleanup(func() { os.Stderr = oldStderr })

	code := Run([]string{noexec}, MergedEnv(nil, nil), "enver x", "p")

	if err := pipeW.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, pipeR); err != nil {
		t.Fatal(err)
	}
	msg := buf.String()
	if code != 127 {
		t.Fatalf("exit = %d, want 127 (stderr: %s)", code, msg)
	}
	if strings.Contains(msg, "command not found") {
		t.Fatalf("a lookup failure that is not absence must not be mislabeled, stderr: %q", msg)
	}
	if !strings.Contains(msg, "permission denied") {
		t.Fatalf("the real lookup error must surface verbatim, stderr: %q", msg)
	}
}

// TestWaitChildSignalExitMapsTo128PlusSignal pins the wait-path exit contract:
// a signal-killed child reports 128+signal like a shell would, not
// ExitCode()'s -1 — which os.Exit renders as 255 with no hint of the signal.
func TestWaitChildSignalExitMapsTo128PlusSignal(t *testing.T) {
	code := waitChild("/bin/sh", []string{"sh", "-c", "kill -TERM $$"}, nil, "enver x")
	if code != 128+15 {
		t.Fatalf("exit = %d, want 128+15 (SIGTERM)", code)
	}
}
