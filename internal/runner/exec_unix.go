//go:build unix

package runner

import (
	"fmt"
	"os"
	"syscall"
)

// execChild replaces the process with the child via execve, so the child owns
// the pty and its exit code is ours.
func execChild(path string, cmdArgs []string, env []string, name string) int {
	if err := syscall.Exec(path, append([]string{cmdArgs[0]}, cmdArgs[1:]...), env); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		return 1
	}
	return 0
}
