//go:build windows

package update

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeArchive returns a goreleaser-style zip containing just the enver.exe
// binary, whose payload is fakeBinaryBody.
func fakeArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("enver.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(fakeBinaryBody)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAcquireLockWindows(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "enver.exe")
	unlock1, err := acquireLock(exe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(exe); err == nil {
		t.Fatal("second exclusive lock must fail")
	}
	unlock1()

	lock := filepath.Join(dir, ".enver-update.lock")
	os.WriteFile(lock, []byte("stale"), 0o600)
	old := time.Now().Add(-11 * time.Minute)
	os.Chtimes(lock, old, old)
	unlock2, err := acquireLock(exe)
	if err != nil {
		t.Fatalf("stale lock must be removed: %v", err)
	}
	unlock2()
}
