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
	build := exec.Command("go", "build", "-o", enverBin, "./cmd/enver")
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
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		switch strings.SplitN(kv, "=", 2)[0] {
		case "HOME", "USERPROFILE", "XDG_CONFIG_HOME", "ENVER_KEY":
			continue
		}
		env = append(env, kv)
	}
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
