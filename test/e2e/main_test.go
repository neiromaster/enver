// Package e2e drives the assembled enver binary as a black box: real process,
// real exit codes, real env ladder, real files on disk.
package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// enverBin is the binary TestMain builds once for the whole suite.
var enverBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "enver-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: temp dir:", err)
		os.Exit(2)
	}

	enverBin = filepath.Join(dir, "enver")
	if runtime.GOOS == "windows" {
		enverBin += ".exe"
	}
	// Coverage runs are keyed on GOCOVERDIR alone: when set, the enver binary
	// is built instrumented and the children contribute covdata through it.
	// Plain builds stay unchanged — an instrumented binary without GOCOVERDIR
	// warns on every child run. The directory must be absolute: go test runs
	// this test binary with the package dir as cwd, so a relative GOCOVERDIR
	// would silently resolve against the wrong base.
	covDir := os.Getenv("GOCOVERDIR")
	buildArgs := []string{"go", "build"}
	if covDir != "" {
		if !filepath.IsAbs(covDir) {
			fmt.Fprintf(os.Stderr, "e2e: GOCOVERDIR must be absolute (go test runs test binaries in the package dir), got: %s\n", covDir)
			os.Exit(2)
		}
		buildArgs = append(buildArgs, "-cover")
	}
	buildArgs = append(buildArgs, "-o", enverBin, "./cmd/enver")
	build := exec.Command(buildArgs[0], buildArgs[1:]...)
	build.Dir = repoRoot()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: build enver: %v\n%s", err, out)
		os.Exit(2)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// repoRoot returns the module root, two levels above this package directory.
func repoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("e2e: cannot locate the source file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

type sandbox struct {
	t       *testing.T
	home    string
	project string
	env     []string
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "proj")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgHome := filepath.Join(home, ".config")
	// Red line: the sandbox must never collide with the real user config.
	if real := os.Getenv("XDG_CONFIG_HOME"); real != "" && real == cfgHome {
		t.Fatalf("sandbox config home %s collides with the real one", cfgHome)
	}
	env := filterEnv(os.Environ(), "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "ENVER_KEY")
	env = append(env,
		"HOME="+home,
		"USERPROFILE="+home,
		"XDG_CONFIG_HOME="+cfgHome,
	)
	return &sandbox{t: t, home: home, project: project, env: env}
}

func (s *sandbox) configPath() string {
	return filepath.Join(s.home, ".config", "enver", "config.yaml")
}

func (s *sandbox) keyPath() string {
	return filepath.Join(s.home, ".config", "enver", "key")
}

func (s *sandbox) localPath() string {
	return filepath.Join(s.project, ".enver.yaml")
}

func (s *sandbox) writeLocal(content string) {
	s.t.Helper()
	s.writeFile(s.localPath(), content)
}

func (s *sandbox) writeGlobal(content string) {
	s.t.Helper()
	s.writeFile(s.configPath(), content)
}

func (s *sandbox) writeFile(path, content string) {
	s.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func (s *sandbox) readFile(path string) string {
	s.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		s.t.Fatal(err)
	}
	return string(data)
}

func (s *sandbox) readLocal() string {
	return s.readFile(s.localPath())
}

func (s *sandbox) setEnv(k, v string) {
	s.env = append(s.env, k+"="+v)
}

// filterEnv drops the named variables from a KEY=VALUE environment.
func filterEnv(env []string, keys ...string) []string {
	dropped := make(map[string]bool, len(keys))
	for _, k := range keys {
		dropped[k] = true
	}
	filtered := make([]string, 0, len(env))
	for _, kv := range env {
		if !dropped[strings.SplitN(kv, "=", 2)[0]] {
			filtered = append(filtered, kv)
		}
	}
	return filtered
}

// dropEnv removes the named variables from the sandbox environment so a test
// can exercise a lower rung of the config-home ladder.
func (s *sandbox) dropEnv(keys ...string) {
	s.t.Helper()
	s.env = filterEnv(s.env, keys...)
}

type result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// run executes the built binary against the sandbox. A timeout kills the
// child and fails the test: a prompt waiting on a missing terminal must not
// hang the suite.
func (s *sandbox) run(args ...string) result {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, enverBin, args...)
	cmd.Dir = s.project
	cmd.Env = s.env
	var out, errB bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errB
	err := cmd.Run()
	if ctx.Err() != nil {
		s.t.Fatalf("enver %v hung past the 10s timeout", args)
	}
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			s.t.Fatalf("enver %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return result{ExitCode: code, Stdout: out.String(), Stderr: errB.String()}
}
