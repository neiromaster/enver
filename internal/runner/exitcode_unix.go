//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

// exitCodeOf maps an ExitError to the child's exit status: the stored code
// when the child exited, or 128+signal when it was killed — the convention
// shells report, where exec.ExitError.ExitCode() alone would give -1.
func exitCodeOf(ee *exec.ExitError) int {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return ee.ExitCode()
}
