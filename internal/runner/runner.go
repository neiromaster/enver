// Package runner executes the target command with a resolved environment.
package runner

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// MergedEnv starts from the current environment and overlays the profile vars,
// returning a sorted "K=V" slice suitable for exec.Cmd.Env.
func MergedEnv(profileEnv map[string]string) []string {
	curMap := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			curMap[k] = v
		}
	}
	for k, v := range profileEnv {
		curMap[k] = v
	}
	keys := make([]string, 0, len(curMap))
	for k := range curMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	res := make([]string, 0, len(keys))
	for _, k := range keys {
		res = append(res, k+"="+curMap[k])
	}
	return res
}

// Run execs cmdArgs with the given environment, wiring stdio through. name is
// the invocation label ("enver x" or "enverx") used to prefix stderr messages.
// It returns the child's exit code (127 if the command is not found).
func Run(cmdArgs []string, env []string, name string) int {
	path, err := exec.LookPath(cmdArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: command not found: %s\n", name, cmdArgs[0])
		return 127
	}
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
