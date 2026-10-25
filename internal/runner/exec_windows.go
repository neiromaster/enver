//go:build windows

package runner

import (
	"os"
	"os/exec"
)

// execChild spawns the child and waits, since Windows has no execve.
func execChild(path string, cmdArgs []string, env []string, name string) int {
	return waitChild(path, cmdArgs, env, name)
}

// lookPathEnv resolves name against the PATH carried by the merged child env
// (a profile that replaces PATH must also steer the lookup). exec.LookPath
// consults only the process environment, so the PATH is swapped for the
// duration of the lookup and restored after — manual scanning is not viable
// without reimplementing Windows PATHEXT resolution. A merged PATH identical
// to the process one skips the swap.
func lookPathEnv(name string, env []string) (string, error) {
	merged := mergedPath(env)
	if merged == os.Getenv("PATH") {
		return exec.LookPath(name)
	}
	old, had := os.LookupEnv("PATH")
	if err := os.Setenv("PATH", merged); err != nil {
		return "", err
	}
	defer func() {
		if had {
			_ = os.Setenv("PATH", old)
		} else {
			_ = os.Unsetenv("PATH")
		}
	}()
	return exec.LookPath(name)
}
