//go:build !windows

package e2e

import "testing"

// lockFileExclusive is only meaningful on Windows; the case that calls it is
// gated by runtime.GOOS, so reaching this stub is a bug in the test. It marks
// the test failed without halting, so the caller body stays reachable to
// staticcheck on non-Windows builds.
func lockFileExclusive(t *testing.T, path string) {
	t.Helper()
	t.Errorf("lockFileExclusive is unsupported on this platform: %s", path)
}
