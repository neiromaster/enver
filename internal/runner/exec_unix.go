//go:build unix

package runner

import (
	"fmt"
	"os"
	"syscall"
)

// execChild replaces the process with the child via execve, so the child owns
// the pty and its exit code is ours. A coverage build (GOCOVERDIR set) waits
// instead: execve never returns, so the exit-time counter flush would be lost
// and instrumented runs would report nothing. Real builds carry no GOCOVERDIR
// and always take the execve path.
func execChild(path string, cmdArgs []string, env []string, name string) int {
	if os.Getenv("GOCOVERDIR") != "" {
		return waitChild(path, cmdArgs, env, name)
	}
	if err := syscall.Exec(path, append([]string{cmdArgs[0]}, cmdArgs[1:]...), env); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		return 1
	}
	return 0
}
