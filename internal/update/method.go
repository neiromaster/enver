package update

import (
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Method identifies how enver was installed.
type Method int

const (
	// MethodDirect is a binary installed outside any package manager.
	MethodDirect Method = iota
	// MethodNPM is an npm install (@enver-go/enver).
	MethodNPM
	// MethodBrew is a Homebrew install of the enver cask.
	MethodBrew
	// MethodGo is a go install github.com/neiromaster/enver/cmd/enver.
	MethodGo
)

func (m Method) String() string {
	switch m {
	case MethodNPM:
		return "npm"
	case MethodBrew:
		return "brew"
	case MethodGo:
		return "go"
	default:
		return "direct"
	}
}

var (
	// LookPath finds commands on PATH; overridable in tests.
	LookPath = exec.LookPath
	// Getenv reads an environment variable; overridable in tests.
	Getenv = os.Getenv
	// RunCommand reports whether a command completed with exit status 0.
	RunCommand = func(name string, args ...string) bool {
		return exec.Command(name, args...).Run() == nil
	}
	// Output runs a command and returns its trimmed stdout.
	Output = func(name string, args ...string) (string, error) {
		out, err := exec.Command(name, args...).Output()
		return strings.TrimSpace(string(out)), err
	}
	// goBinDir returns where `go install` writes binaries: $GOBIN, else
	// $GOPATH/bin. Overridable in tests.
	goBinDir = func() (string, bool) {
		if g := Getenv("GOBIN"); g != "" {
			return g, true
		}
		if gopath := build.Default.GOPATH; gopath != "" {
			return filepath.Join(gopath, "bin"), true
		}
		return "", false
	}
)

// brewPath returns the brew binary: the one on PATH, else the two standard
// Homebrew prefixes.
func brewPath() string {
	if p, err := LookPath("brew"); err == nil {
		return p
	}
	for _, p := range []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ResolveMethod reports how the binary at exe was installed. exe must be the
// symlink-resolved executable path (see cmd/enver execPath).
func ResolveMethod(exe string) Method {
	if strings.Contains(filepath.ToSlash(exe), "/node_modules/@enver-go") {
		return MethodNPM
	}
	if b := brewPath(); b != "" && RunCommand(b, "list", "enver") {
		return MethodBrew
	}
	if dir, ok := goBinDir(); ok && within(dir, exe) {
		return MethodGo
	}
	return MethodDirect
}

// within reports whether path is inside dir.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// Command returns the owning package manager's update invocation for this
// install; ok is false for MethodDirect, which self-replaces instead.
func (m Method) Command(exe string) (name string, args []string, ok bool) {
	switch m {
	case MethodNPM:
		return npmCommand(exe)
	case MethodBrew:
		return "brew", []string{"upgrade", "enver"}, true
	case MethodGo:
		return "go", []string{"install", "github.com/neiromaster/enver/cmd/enver@latest"}, true
	default:
		return "", nil, false
	}
}

// npmCommand decides global vs local: a global install lives under `npm root
// -g`; anything else under node_modules is a local install whose project root
// is the path before /node_modules/.
func npmCommand(exe string) (string, []string, bool) {
	if g, err := Output("npm", "root", "-g"); err == nil && withinPrefix(g, exe) {
		return "npm", []string{"update", "-g", "@enver-go/enver"}, true
	}
	i := strings.Index(filepath.ToSlash(exe), "/node_modules/")
	if i < 0 {
		return "", nil, false
	}
	projectRoot := filepath.FromSlash(filepath.ToSlash(exe)[:i])
	return "npm", []string{"update", "@enver-go/enver", "--prefix", projectRoot}, true
}

// withinPrefix reports whether path is inside dir, slash-normalized (so the
// npm root -g result compares correctly on Windows too).
func withinPrefix(dir, path string) bool {
	dir = strings.TrimSuffix(filepath.ToSlash(dir), "/")
	path = filepath.ToSlash(path)
	return path == dir || strings.HasPrefix(path, dir+"/")
}
