//go:build windows

package update

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// acquireLock creates a lock file next to exe exclusively (O_EXCL), removing
// one left behind by a crashed run (older than 10 minutes). A concurrent
// update fails fast with a message. The returned func removes the lock.
func acquireLock(exe string) (func(), error) {
	lock := filepath.Join(filepath.Dir(exe), ".enver-update.lock")
	for attempts := 0; attempts < 2; attempts++ {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(lock) }, nil
		}
		if os.IsExist(err) {
			if staleLock(lock) {
				_ = os.Remove(lock)
				continue
			}
			return nil, fmt.Errorf("another enver update is already running")
		}
		return nil, fmt.Errorf("create update lock: %w", err)
	}
	return nil, fmt.Errorf("another enver update is already running")
}

// staleLock reports whether the lock file predates the 10-minute crash window.
func staleLock(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return false
	}
	return time.Since(fi.ModTime()) > 10*time.Minute
}

// extractBinary pulls the enver.exe member out of a goreleaser zip into dest.
func extractBinary(data []byte, dest string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if filepath.Base(f.Name) == "enver.exe" {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer func() { _ = rc.Close() }()
			return writeBinary(dest, rc)
		}
	}
	return fmt.Errorf("binary not found in archive")
}

// replace moves the running exe to .old (Windows cannot rename over an open
// file), moves tmp into place, then removes .old best-effort. A leftover .old
// from a crashed run is cleaned up here too.
func replace(exe, tmp string) error {
	dir := filepath.Dir(exe)
	old := filepath.Join(dir, filepath.Base(exe)+".old")
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		return fmt.Errorf("rename %s: %w", exe, err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Rename(old, exe)
		return fmt.Errorf("replace %s: %w", exe, err)
	}
	_ = os.Remove(old)
	return nil
}
