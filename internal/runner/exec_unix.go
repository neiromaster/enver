//go:build unix

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// lookPathEnv resolves name the way the child will see it: against the PATH
// the merged env carries, not enver's own process PATH — a profile that
// replaces PATH must also steer the lookup. A merged PATH identical to the
// process one keeps stdlib resolution; a differing one is resolved by scanning
// the merged list directly, which leaves the process environment untouched
// (an os.Setenv swap would be a process-wide side effect and a race under
// concurrent callers).
func lookPathEnv(name string, env []string) (string, error) {
	merged := mergedPath(env)
	if merged == os.Getenv("PATH") {
		return exec.LookPath(name)
	}
	return lookPathIn(name, merged)
}

// lookPathIn mirrors exec.LookPath's unix semantics against an explicit PATH
// list: names with a separator resolve directly, empty entries mean ".", a
// candidate needs any execute bit, and a relative resolution reports ErrDot.
func lookPathIn(file, path string) (string, error) {
	if strings.Contains(file, "/") {
		if err := findExecutable(file); err != nil {
			return "", &exec.Error{Name: file, Err: err}
		}
		return file, nil
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			dir = "."
		}
		p := filepath.Join(dir, file)
		if err := findExecutable(p); err == nil {
			if !filepath.IsAbs(p) {
				return p, &exec.Error{Name: file, Err: exec.ErrDot}
			}
			return p, nil
		}
	}
	return "", &exec.Error{Name: file, Err: exec.ErrNotFound}
}

func findExecutable(file string) error {
	info, err := os.Stat(file)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return syscall.EISDIR
	}
	if info.Mode().Perm()&0o111 != 0 {
		return nil
	}
	return syscall.EACCES
}

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
