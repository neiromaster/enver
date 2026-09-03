package e2e

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ladderYAML seeds one profile; decoyYAML marks a config planted on a rung the
// test expects to lose. Whichever rung wins, its marker must be the one listed.
const (
	ladderYAML = "profiles:\n  ladder:\n    env:\n      LADDER: \"1\"\n"
	decoyYAML  = "profiles:\n  decoy:\n    env:\n      DECOY: \"1\"\n"
)

// fallbackHint is the exact bottom-rung resolution: with no home at all, xdg
// falls back to "/", so the hint names the "/"-rooted config path. It is
// left-anchored on the "Create one at: " prefix because a fallback regressed
// to the working directory would carry the same tail.
var fallbackHint = "Create one at: " + filepath.Join("/", ".config", "enver", "config.yaml")

// platformHomeRungWritable reports whether the rung below HOME resolves to a
// real directory on this platform: os.UserHomeDir reads USERPROFILE on
// Windows. On POSIX it reads $HOME only and errors once HOME is dropped, so
// the rung degrades to the "/"-fallback hint — observable, not writable.
func platformHomeRungWritable() bool { return runtime.GOOS == "windows" }

// assertListed requires the run to exit 0, its output to carry want, and —
// unless notWant is empty — not to carry notWant.
func assertListed(t *testing.T, r result, want, notWant string) {
	t.Helper()
	if r.ExitCode != 0 {
		t.Fatalf("list -g exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, want) {
		t.Fatalf("list -g output lacks %q, got: %q (stderr: %s)", want, r.Stdout, r.Stderr)
	}
	if notWant != "" && strings.Contains(r.Stdout, notWant) {
		t.Fatalf("list -g output must not carry %q, got: %q", notWant, r.Stdout)
	}
}

// TestConfigHomeRungXDG pins the top rung: XDG_CONFIG_HOME beats HOME even
// when both point at different seeded configs.
func TestConfigHomeRungXDG(t *testing.T) {
	s := newSandbox(t)
	s.dropEnv("XDG_CONFIG_HOME")
	xdgAlt := filepath.Join(s.home, "xdg-alt")
	s.setEnv("XDG_CONFIG_HOME", xdgAlt)
	s.writeFile(filepath.Join(xdgAlt, "enver", "config.yaml"), ladderYAML)
	s.writeGlobal(decoyYAML) // HOME/.config — must lose to XDG

	assertListed(t, s.run("list", "-g"), "ladder", "decoy")
}

// TestConfigHomeRungHOME pins the middle rung, guarding the precedence
// regression where the platform home beats HOME: with XDG dropped, HOME must
// win over the platform home.
func TestConfigHomeRungHOME(t *testing.T) {
	s := newSandbox(t)
	s.dropEnv("XDG_CONFIG_HOME", "USERPROFILE")
	upAlt := filepath.Join(s.home, "up-alt")
	s.setEnv("USERPROFILE", upAlt)
	s.writeFile(filepath.Join(upAlt, ".config", "enver", "config.yaml"), decoyYAML)
	s.writeGlobal(ladderYAML) // HOME/.config — must beat USERPROFILE

	assertListed(t, s.run("list", "-g"), "ladder", "decoy")
}

// TestConfigHomeRungPlatformHome pins the platform-home rung, guarding the
// home-resolution regression where os.UserHomeDir is never consulted: with
// XDG and HOME dropped, os.UserHomeDir resolves the config home (USERPROFILE
// on Windows). POSIX degrades to the "/"-fallback hint.
func TestConfigHomeRungPlatformHome(t *testing.T) {
	s := newSandbox(t)
	s.dropEnv("XDG_CONFIG_HOME", "HOME")
	if platformHomeRungWritable() {
		s.writeGlobal(ladderYAML) // USERPROFILE/.config
	}
	r := s.run("list", "-g")
	if r.ExitCode != 0 {
		t.Fatalf("list -g exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if platformHomeRungWritable() {
		if !strings.Contains(r.Stdout, "ladder") {
			t.Fatalf("USERPROFILE rung must resolve the config, got: %q", r.Stdout)
		}
		return
	}
	assertListed(t, r, fallbackHint, "")
}

// TestConfigHomeRungFallbackHint pins the bottom rung: with every home
// variable dropped, os.UserHomeDir errors and xdg falls back to "/". No
// writes anywhere — machine paths are off limits, nothing is written.
func TestConfigHomeRungFallbackHint(t *testing.T) {
	s := newSandbox(t)
	s.dropEnv("XDG_CONFIG_HOME", "HOME", "USERPROFILE")
	assertListed(t, s.run("list", "-g"), fallbackHint, "")
}
