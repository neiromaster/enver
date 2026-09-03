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

// platformHomeRungWritable reports whether the rung below HOME resolves to a
// real directory on this platform: os.UserHomeDir reads USERPROFILE on
// Windows. On POSIX it reads $HOME only and errors once HOME is dropped, so
// the rung degrades to the "/"-fallback hint — observable, not writable.
func platformHomeRungWritable() bool { return runtime.GOOS == "windows" }

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

// TestConfigHomeRungHOME pins the middle rung (the 125b3df precedence): with
// XDG dropped, HOME wins over the platform home.
func TestConfigHomeRungHOME(t *testing.T) {
	s := newSandbox(t)
	s.dropEnv("XDG_CONFIG_HOME", "USERPROFILE")
	upAlt := filepath.Join(s.home, "up-alt")
	s.setEnv("USERPROFILE", upAlt)
	s.writeFile(filepath.Join(upAlt, ".config", "enver", "config.yaml"), decoyYAML)
	s.writeGlobal(ladderYAML) // HOME/.config — must beat USERPROFILE

	assertListed(t, s.run("list", "-g"), "ladder", "decoy")
}

// TestConfigHomeRungPlatformHome pins the df72d82 rung: with XDG and HOME
// dropped, os.UserHomeDir resolves the config home (USERPROFILE on Windows).
// POSIX degrades to the "/"-fallback hint.
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
	if !strings.Contains(r.Stdout, "config.yaml") {
		t.Fatalf("POSIX hint must name config.yaml, got: %q", r.Stdout)
	}
}

// TestConfigHomeRungFallbackHint pins the bottom rung: with every home
// variable dropped, os.UserHomeDir errors and xdg falls back to "/", so the
// hint names a config.yaml. No writes anywhere — the "/" root is not writable
// in CI and machine paths are off limits.
func TestConfigHomeRungFallbackHint(t *testing.T) {
	s := newSandbox(t)
	s.dropEnv("XDG_CONFIG_HOME", "HOME", "USERPROFILE")
	r := s.run("list", "-g")
	if r.ExitCode != 0 {
		t.Fatalf("list -g exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "config.yaml") {
		t.Fatalf("the fallback hint must name config.yaml, got: %q", r.Stdout)
	}
}
