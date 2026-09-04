//go:build windows

package runner

// execChild spawns the child and waits, since Windows has no execve.
func execChild(path string, cmdArgs []string, env []string, name string) int {
	return waitChild(path, cmdArgs, env, name)
}
