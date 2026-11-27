package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ansiCSI matches the terminal control sequences a PowerShell console host
// sprinkles into stdout when its stdin is a pipe. They are harness noise, not
// output, so assertions never see them.
var ansiCSI = regexp.MustCompile("\x1b\\[[0-9;?]*[A-Za-z]")

// powerShellEngines lists the PowerShell executables installed on this host:
// pwsh everywhere, plus Windows PowerShell on Windows only. Callers skip when
// the result is empty.
func powerShellEngines(t *testing.T) []string {
	t.Helper()
	names := []string{"pwsh"}
	if runtime.GOOS == "windows" {
		names = append(names, "powershell")
	}
	var engines []string
	for _, name := range names {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		engines = append(engines, path)
	}
	return engines
}

// runPowerShell feeds stdin to an engine as commands. Execution policy governs
// script files, not commands, which is exactly why the one-line irm | iex
// installer works for users under Restricted; the harness rides the same path.
func runPowerShell(t *testing.T, engine, policy, stdin string, env map[string]string) (string, error) {
	t.Helper()
	if policy == "" {
		policy = "Bypass"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, engine, "-NoProfile", "-ExecutionPolicy", policy, "-Command", "-")
	cmd.Stdin = strings.NewReader(stdin)

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	cmd.Env = filterEnv(os.Environ(), keys...)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var out, errB bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errB
	err := cmd.Run()
	stdout := ansiCSI.ReplaceAllString(out.String(), "")
	if ctx.Err() != nil {
		return stdout, fmt.Errorf("%s hung past the 60s timeout", filepath.Base(engine))
	}
	if err != nil {
		return stdout, fmt.Errorf("%s: %w\nstdout:\n%s\nstderr:\n%s",
			filepath.Base(engine), err, stdout, ansiCSI.ReplaceAllString(errB.String(), ""))
	}
	return stdout, nil
}

// forEachPowerShellEngine runs body once per engine in its own subtest and temp
// dir, so file assertions never see another engine leftovers. Failures inside
// body must report on the subtest t it is handed, not on the parent.
func forEachPowerShellEngine(t *testing.T, body func(t *testing.T, engine, dir string)) {
	t.Helper()
	engines := powerShellEngines(t)
	if len(engines) == 0 {
		t.Skip("no PowerShell engine found on this host")
	}
	for _, engine := range engines {
		engine := engine
		t.Run(filepath.Base(engine), func(t *testing.T) {
			body(t, engine, t.TempDir())
		})
	}
}

// installScriptText returns the installer source, so a test can feed it to an
// engine with the flow disabled and call the helpers it defines.
func installScriptText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(), "scripts", "install.ps1"))
	if err != nil {
		t.Fatalf("read scripts/install.ps1: %v", err)
	}
	return string(data)
}
