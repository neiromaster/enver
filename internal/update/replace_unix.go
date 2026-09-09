//go:build unix

package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// acquireLock takes an exclusive non-blocking flock on a lock file next to
// exe. flock auto-releases on process death; a concurrent update fails fast
// with a message. The lock file is left in place (flock owns the lock, not
// the file). The returned func releases the lock.
func acquireLock(exe string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(filepath.Dir(exe), ".enver-update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create update lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("another enver update is already running")
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// extractBinary pulls the enver member out of a goreleaser tar.gz into dest.
func extractBinary(data []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("binary not found in archive")
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Base(hdr.Name) == "enver" {
			return writeBinary(dest, tr)
		}
	}
}

// replace atomically moves tmp over exe.
func replace(exe, tmp string) error {
	if err := os.Rename(tmp, exe); err != nil {
		return fmt.Errorf("replace %s: %w", exe, err)
	}
	return nil
}
