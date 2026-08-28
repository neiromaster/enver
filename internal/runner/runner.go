// Package runner executes the target command with a resolved environment.
package runner

import (
	"fmt"
	"os"
	"os/exec"
	"slices"

	"github.com/neiromaster/enver/internal/envname"
	"golang.org/x/term"
)

// MergedEnv overlays profileEnv on osEnv and returns a sorted "K=V" slice
// suitable for exec.Cmd.Env. The profile overlay is case-aware on Windows: a
// profile's Path replaces the shell's PATH rather than shadowing it as a
// second key. Unset keys are simply absent from profileEnv — the resolved env
// never carries them — so a shell-exported value passes through untouched.
func MergedEnv(osEnv, profileEnv map[string]string) []string {
	curMap := make(map[string]string, len(osEnv)+len(profileEnv))
	for k, v := range osEnv {
		curMap[k] = v
	}
	for k, v := range profileEnv {
		envname.Set(curMap, k, v)
	}
	keys := make([]string, 0, len(curMap))
	for k := range curMap {
		keys = append(keys, k)
	}
	slices.Sort(keys)
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
