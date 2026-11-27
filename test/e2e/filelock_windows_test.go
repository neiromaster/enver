//go:build windows

package e2e

import (
	"testing"

	"golang.org/x/sys/windows"
)

// lockFileExclusive opens path with no sharing, so Windows refuses to delete it
// the way it refuses to delete a running enver.exe. The handle is closed when
// the test and its temp dir are torn down.
func lockFileExclusive(t *testing.T, path string) {
	t.Helper()
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("utf16 path %s: %v", path, err)
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ, 0, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("open %s without sharing: %v", path, err)
	}
	t.Cleanup(func() {
		_ = windows.CloseHandle(handle)
	})
}
