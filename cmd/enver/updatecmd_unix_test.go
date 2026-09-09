//go:build unix

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/neiromaster/enver/internal/update"
	"github.com/neiromaster/enver/internal/version"
)

func TestRunUpdateSelfReplacesDirectBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "enver")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	body := "new-enver-binary"
	data := tarGzBinary(t, "enver", body)
	archive := fmt.Sprintf("enver_0.9.1_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	sum := fmt.Sprintf("%x", sha256.Sum256(data))
	assets := map[string]string{
		"/neiromaster/enver/releases/download/v0.9.1/checksums.txt": fmt.Sprintf("%s  %s\n", sum, archive),
		"/neiromaster/enver/releases/download/v0.9.1/" + archive:    string(data),
	}

	oldPath, oldVersion := execPath, version.Version
	execPath = func() (string, error) { return exe, nil }
	version.Version = "0.0.1"
	t.Cleanup(func() { execPath, version.Version = oldPath, oldVersion })

	oldLook, oldRun := update.LookPath, update.RunCommand
	update.LookPath = func(string) (string, error) { return "", os.ErrNotExist }
	update.RunCommand = func(string, ...string) bool { return false }
	t.Cleanup(func() { update.LookPath, update.RunCommand = oldLook, oldRun })

	pointClientAt(t, mockGitHub(t, "v0.9.1", assets))
	if err := runUpdate(false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("updated binary = %q, want %q", got, body)
	}
}

func tarGzBinary(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
