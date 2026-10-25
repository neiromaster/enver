//go:build windows

package runner

import "os/exec"

// exitCodeOf maps an ExitError to the child's exit status. Windows has no
// signal status; the stored code is the whole truth.
func exitCodeOf(ee *exec.ExitError) int {
	return ee.ExitCode()
}
