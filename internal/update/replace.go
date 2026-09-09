package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// artifactName is the goreleaser asset name for this platform and tag, e.g.
// enver_0.9.1_darwin_arm64.tar.gz.
func artifactName(tag string) string {
	v := strings.TrimPrefix(tag, "v")
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("enver_%s_%s_%s.%s", v, runtime.GOOS, runtime.GOARCH, ext)
}

// SelfReplace downloads the release for tag and atomically replaces the binary
// at exe after verifying the archive's SHA-256 against checksums.txt.
func SelfReplace(c *Client, exe, tag string) error {
	unlock, err := acquireLock(exe)
	if err != nil {
		return err
	}
	defer unlock()

	sums, err := c.Checksums(tag)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	archive := artifactName(tag)
	want, ok := sums[archive]
	if !ok {
		return fmt.Errorf("checksums.txt has no entry for %s", archive)
	}
	data, err := c.Download(tag, archive)
	if err != nil {
		return fmt.Errorf("download %s: %w", archive, err)
	}
	if got := sha256Hex(data); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", archive, got, want)
	}
	return swapExecutable(exe, data)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// writeBinary writes src to dest with 0755 permissions.
func writeBinary(dest string, src io.Reader) error {
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, src); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// swapExecutable writes the archive data to a temp file next to exe (same
// filesystem, so the final rename is atomic), chmods it executable, and
// calls the platform replace.
func swapExecutable(exe string, data []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".enver-update-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if err := extractBinary(data, tmp.Name()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil { //nolint:gosec
		return err
	}
	return replace(exe, tmp.Name())
}
