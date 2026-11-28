package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

// File-flag tests: --file points every command at one config file as the whole
// config — resolution order, the store it redirects, and the helpers every
// command resolves through.

func resetFile(t *testing.T, file string) string {
	t.Helper()
	old := globalFlags
	oldDir := config.RemotesDir
	t.Cleanup(func() { globalFlags = old; config.RemotesDir = oldDir; config.HintFlags = "" })
	globalFlags = globalOptions{}
	if file != "" {
		globalFlags.file = file
	}
	return file
}

func TestHintFlags(t *testing.T) {
	cases := []struct {
		name string
		g    globalOptions
		want string
	}{
		{"plain", globalOptions{}, ""},
		{"chdir", globalOptions{chdir: "sub"}, "--chdir sub"},
		{"config", globalOptions{configPath: "c.yaml"}, "--config c.yaml"},
		{"file", globalOptions{file: "x.yaml"}, "--file x.yaml"},
		{"chdir+file", globalOptions{chdir: "sub", file: "x.yaml"}, "--chdir sub --file x.yaml"},
		{"chdir+config", globalOptions{chdir: "sub", configPath: "c.yaml"}, "--chdir sub --config c.yaml"},
		{"whitespace", globalOptions{chdir: "my dir", file: "team.yaml"}, `--chdir "my dir" --file team.yaml`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			old := globalFlags
			t.Cleanup(func() { globalFlags = old })
			globalFlags = c.g
			if got := hintFlags(); got != c.want {
				t.Fatalf("hintFlags() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestHintPath pins the spelling of a path inside a hint line: bare without
// whitespace, otherwise double-quoted. The quoted text is the path verbatim on
// Windows, where backslash is a literal in every shell and in Go's own argv
// parsing; strconv.Quote's escaping stays for POSIX shells. The backslash
// whitespace path distinguishes the branches: Quote alone would double every
// separator on Windows.
func TestHintPath(t *testing.T) {
	if got := hintPath("sub/x.yaml"); got != "sub/x.yaml" {
		t.Fatalf("hintPath(%q) = %q, want it bare", "sub/x.yaml", got)
	}
	p := `C:\Users\my dir`
	want := `"` + p + `"`
	if runtime.GOOS != "windows" {
		want = strconv.Quote(p)
	}
	if got := hintPath(p); got != want {
		t.Fatalf("hintPath(%q) = %q, want %q", p, got, want)
	}
}

func TestApplyFileConflicts(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func()
	}{
		{"config", func() { globalFlags.configPath = "other.yaml" }},
		{"no-local", func() { globalFlags.noLocal = true }},
		{"global", func() { globalFlags.global = true }},
	} {
		t.Run(c.name, func(t *testing.T) {
			resetFile(t, "x.yaml")
			c.set()
			err := applyFile()
			if err == nil || !strings.Contains(err.Error(), c.name) {
				t.Fatalf("applyFile() with --%s = %v, want an error naming the flag", c.name, err)
			}
		})
	}
}

func TestApplyFileResolvesAgainstChdir(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// EvalSymlinks: on macOS t.TempDir() returns /var/... but Getwd resolves the
	// /var → /private/var symlink, so the store compare holds.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Restore cwd before TempDir's cleanup removes dir: cleanups run LIFO, and
	// Windows cannot remove a directory that is still the process cwd.
	t.Cleanup(func() { _ = os.Chdir(wd) })
	resetFile(t, filepath.Join("sub", "team.yaml"))
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	globalFlags.chdir = dir
	if err := applyGlobalFlags(); err != nil {
		t.Fatal(err)
	}
	if got := config.RemotesDir(); got != filepath.Join(dir, "sub", ".enver", "team.yaml") {
		t.Fatalf("store = %q", got)
	}
}

func TestApplyFileRefusesDirectoryAndDotEnver(t *testing.T) {
	resetFile(t, t.TempDir())
	if err := applyFile(); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Fatalf("a directory as --file: %v", err)
	}
	en := filepath.Join(t.TempDir(), ".enver")
	if err := os.WriteFile(en, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	resetFile(t, filepath.Join(filepath.Dir(en), "x.yaml"))
	if err := applyFile(); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("a .enver file beside X: %v", err)
	}
	parent := filepath.Join(t.TempDir(), "missing", "x.yaml")
	resetFile(t, parent)
	if err := applyFile(); err == nil || !strings.Contains(err.Error(), "parent directory") {
		t.Fatalf("a missing parent directory: %v", err)
	}
}

func TestAppOptsUnderFile(t *testing.T) {
	resetFile(t, "x.yaml")
	o := appOpts()
	if o.FilePath != "x.yaml" || o.ConfigPath != "" || o.NoLocal {
		t.Fatalf("appOpts() = %+v", o)
	}
}

func TestWriteTargetAndAddCommandLineUnderFile(t *testing.T) {
	resetFile(t, "x.yaml")
	if err := applyGlobalFlags(); err != nil {
		t.Fatal(err)
	}
	if got := writeTarget(); got != "x.yaml" {
		t.Fatalf("writeTarget() = %q", got)
	}
	if got := addCommandLine("p"); got != "enver add --file x.yaml p" {
		t.Fatalf("addCommandLine() = %q", got)
	}
}

func TestAddCommandLineCarriesRelocationFlags(t *testing.T) {
	old := globalFlags
	t.Cleanup(func() { globalFlags = old; config.HintFlags = "" })
	globalFlags = globalOptions{chdir: "sub", configPath: "c.yaml", global: true}
	config.HintFlags = hintFlags()
	if got := addCommandLine("p"); got != "enver add --chdir sub --config c.yaml -g p" {
		t.Fatalf("addCommandLine() = %q", got)
	}
	globalFlags = globalOptions{global: true}
	config.HintFlags = hintFlags()
	if got := addCommandLine("p"); got != "enver add -g p" {
		t.Fatalf("addCommandLine() without relocation flags = %q", got)
	}
}

func TestLayerLabelUnderFile(t *testing.T) {
	resetFile(t, "")
	if layerLabel(config.LayerGlobal) != config.LayerGlobal {
		t.Fatal("without --file the label is unchanged")
	}
	globalFlags.file = "x.yaml"
	if layerLabel(config.LayerGlobal) != config.LayerFile || layerLabel("remote:r") != "remote:r" {
		t.Fatalf("layerLabel under --file = %q / %q", layerLabel(config.LayerGlobal), layerLabel("remote:r"))
	}
}

func TestScanCryptIncludesFile(t *testing.T) {
	resetFile(t, "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	globalFlags.file = filepath.Join(t.TempDir(), "team.yaml")
	if err := os.WriteFile(globalFlags.file, []byte("profiles:\n  p:\n    env:\n      K: "+sealedValue(t, "v")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scan, err := scanCryptForApp()
	if err != nil || scan.Salt == nil {
		t.Fatalf("scan did not see the file's ciphertext: %+v, %v", scan, err)
	}
}
