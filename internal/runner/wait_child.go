package runner

import (
	"fmt"
	"os"
	"os/exec"
)

// waitChild spawns the child and waits for it. Windows uses it always, having
// no execve; unix uses it for coverage builds, where execve would replace the
// process and lose the exit-time counter flush. The child inherits stdio; its
// exit code is returned. Args keeps the typed name as argv[0], matching the
// unix execve path.
func waitChild(path string, cmdArgs []string, env []string, name string) int {
	cmd := &exec.Cmd{Path: path, Args: cmdArgs, Env: env, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		return 1
	}
	return 0
}
