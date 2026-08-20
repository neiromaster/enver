//go:build windows

package runner

import (
	"fmt"
	"os"
	"os/exec"
)

// execChild spawns the child and waits, since Windows has no execve. The child
// inherits stdio; its exit code is returned.
func execChild(path string, cmdArgs []string, env []string, name string) int {
	cmd := exec.Command(path, cmdArgs[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		return 1
	}
	return 0
}
