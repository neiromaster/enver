//go:build unix

package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

// fakeArchive returns a goreleaser-style tar.gz containing just the enver
// binary, whose payload is fakeBinaryBody.
func fakeArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name: "enver", Mode: 0o755, Size: int64(len(fakeBinaryBody)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(fakeBinaryBody)); err != nil {
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

func TestAcquireLockUnix(t *testing.T) {
	exe := t.TempDir() + "/enver"
	unlock1, err := acquireLock(exe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireLock(exe); err == nil {
		t.Fatal("second concurrent flock must fail")
	}
	unlock1()
	unlock2, err := acquireLock(exe)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	unlock2()
}
