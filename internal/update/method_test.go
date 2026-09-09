package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMethod(t *testing.T) {
	cases := []struct {
		name  string
		exe   string
		setup func()
		want  Method
	}{
		{
			name: "npm path",
			exe:  filepath.Join("home", "u", "node_modules", "@enver-go", "enver-darwin-arm64", "bin", "enver"),
			setup: func() {
				LookPath = func(string) (string, error) { return "", os.ErrNotExist }
				RunCommand = func(string, ...string) bool { return false }
			},
			want: MethodNPM,
		},
		{
			name: "brew installed",
			exe:  filepath.Join("/opt", "homebrew", "Cellar", "enver", "0.9.1", "bin", "enver"),
			setup: func() {
				LookPath = func(string) (string, error) { return "/opt/homebrew/bin/brew", nil }
				RunCommand = func(name string, args ...string) bool {
					return name == "/opt/homebrew/bin/brew" && len(args) == 2 &&
						args[0] == "list" && args[1] == "enver"
				}
				Output = func(name string, args ...string) (string, error) { return "/opt/homebrew", nil }
			},
			want: MethodBrew,
		},
		{
			name: "brew installed but exe elsewhere",
			exe:  filepath.Join("/home", "u", ".local", "bin", "enver"),
			setup: func() {
				LookPath = func(string) (string, error) { return "/opt/homebrew/bin/brew", nil }
				RunCommand = func(name string, args ...string) bool {
					return name == "/opt/homebrew/bin/brew" && len(args) == 2 &&
						args[0] == "list" && args[1] == "enver"
				}
				Output = func(name string, args ...string) (string, error) { return "/opt/homebrew", nil }
				goBinDir = func() (string, bool) { return filepath.Join("/home", "u", "go", "bin"), true }
			},
			want: MethodDirect,
		},
		{
			name: "go bin",
			exe:  filepath.Join("home", "u", "go", "bin", "enver"),
			setup: func() {
				LookPath = func(string) (string, error) { return "", os.ErrNotExist }
				RunCommand = func(string, ...string) bool { return false }
				goBinDir = func() (string, bool) { return filepath.Join("home", "u", "go", "bin"), true }
			},
			want: MethodGo,
		},
		{
			name: "direct",
			exe:  filepath.Join("usr", "local", "bin", "enver"),
			setup: func() {
				LookPath = func(string) (string, error) { return "", os.ErrNotExist }
				RunCommand = func(string, ...string) bool { return false }
				goBinDir = func() (string, bool) { return filepath.Join("home", "u", "go", "bin"), true }
			},
			want: MethodDirect,
		},
	}
	origLook, origRun, origGo, origOut := LookPath, RunCommand, goBinDir, Output
	t.Cleanup(func() { LookPath, RunCommand, goBinDir, Output = origLook, origRun, origGo, origOut })

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			LookPath, RunCommand, goBinDir, Output = origLook, origRun, origGo, origOut
			if c.setup != nil {
				c.setup()
			}
			if got := ResolveMethod(c.exe); got != c.want {
				t.Errorf("ResolveMethod(%s) = %v, want %v", c.exe, got, c.want)
			}
		})
	}
}

func TestNPMCommandGlobal(t *testing.T) {
	gbin := filepath.Join("home", "u", "npm", "global")
	exe := filepath.Join(gbin, "@enver-go", "enver-darwin-arm64", "bin", "enver")
	orig := Output
	Output = func(name string, args ...string) (string, error) { return gbin, nil }
	t.Cleanup(func() { Output = orig })

	name, args, ok := MethodNPM.Command(exe)
	if !ok {
		t.Fatal("global npm detected as not manageable")
	}
	if name != "npm" || len(args) != 3 || args[0] != "update" || args[1] != "-g" || args[2] != "@enver-go/enver" {
		t.Errorf("global npm command = %s %v, want npm update -g @enver-go/enver", name, args)
	}
}

func TestNPMCommandLocal(t *testing.T) {
	proj := filepath.Join("work", "project")
	exe := filepath.Join(proj, "node_modules", "@enver-go", "enver-darwin-arm64", "bin", "enver")
	orig := Output
	Output = func(name string, args ...string) (string, error) { return "", os.ErrNotExist }
	t.Cleanup(func() { Output = orig })

	name, args, ok := MethodNPM.Command(exe)
	if !ok {
		t.Fatal("local npm detected as not manageable")
	}
	if name != "npm" || len(args) != 4 || args[2] != "--prefix" || args[3] != proj {
		t.Errorf("local npm command = %s %v, want --prefix %s", name, args, proj)
	}
}

func TestDirectHasNoCommand(t *testing.T) {
	if _, _, ok := MethodDirect.Command("/some/bin/enver"); ok {
		t.Fatal("direct install must not delegate")
	}
}
