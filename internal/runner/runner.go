// Package runner executes the target command with a resolved environment.
package runner

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"sort"

	"golang.org/x/term"
)

// MergedEnv overlays profileEnv on osEnv, drops the unsets, and returns a
// sorted "K=V" slice suitable for exec.Cmd.Env. Unsets remove a key even when
// the shell exports it — that is the point of an unset in the config.
func MergedEnv(osEnv, profileEnv map[string]string, unsets []string) []string {
	curMap := make(map[string]string, len(osEnv)+len(profileEnv))
	maps.Copy(curMap, osEnv)
	maps.Copy(curMap, profileEnv)
	for _, u := range unsets {
		delete(curMap, u)
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
// the invocation label ("enver x") used to prefix stderr messages.
// profile is shown in the terminal title until the child sends its own. It
// returns the child's exit code (127 if the command is not found).
func Run(cmdArgs []string, env []string, name, profile string) int {
	path, err := exec.LookPath(cmdArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: command not found: %s\n", name, cmdArgs[0])
		return 127
	}
	if term.IsTerminal(int(os.Stdout.Fd())) {
		_, _ = os.Stdout.WriteString(launchTitleOSC(profile))
	}
	return execChild(path, cmdArgs, env, name)
}

// launchTitleOSC primes the terminal title before exec: the first OSC 0 locks
// VS Code's agent mode (matched by /claude\s*code/i), the second shows the
// profile name until the child overwrites it with its own titles.
func launchTitleOSC(profile string) string {
	return "\x1b]0;claude code\x07\x1b]0;" + profile + "\x07"
}
