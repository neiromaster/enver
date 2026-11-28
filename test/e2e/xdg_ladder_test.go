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

// emptyListText is the empty-config listing's anchor line. The rung tests
// pair it with a decoy: a rung that regressed to the working directory or to
// USERPROFILE would list the decoy profile instead of the empty text.
const emptyListText = "(no profiles defined)"

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
// XDG and HOME dropped, os.UserHomeDir resolves the config home. Windows
// reads USERPROFILE, so a config planted there must win; POSIX reads $HOME
// only (already dropped), so the rung degrades to the bottom rung — and the
// still-set USERPROFILE must stay unconsulted, so the decoy planted there
// stays unlisted while the empty text renders.
func TestConfigHomeRungPlatformHome(t *testing.T) {
	s := newSandbox(t)
	s.dropEnv("XDG_CONFIG_HOME", "HOME")
	if runtime.GOOS == "windows" {
		s.writeGlobal(ladderYAML) // USERPROFILE/.config
	} else {
		upAlt := filepath.Join(s.home, "up-alt")
		s.setEnv("USERPROFILE", upAlt)
		s.writeFile(filepath.Join(upAlt, ".config", "enver", "config.yaml"), decoyYAML)
	}
	r := s.run("list", "-g")
	if r.ExitCode != 0 {
		t.Fatalf("list -g exit = %d, stderr: %s", r.ExitCode, r.Stderr)
	}
	if runtime.GOOS == "windows" {
		if !strings.Contains(r.Stdout, "ladder") {
			t.Fatalf("USERPROFILE rung must resolve the config, got: %q", r.Stdout)
		}
		return
	}
	assertListed(t, r, emptyListText, "decoy")
}

// TestConfigHomeRungFallbackHint pins the bottom rung: with every home
// variable dropped, os.UserHomeDir errors and xdg falls back to "/". No
// writes anywhere — machine paths are off limits, nothing is written. The
// local decoy must stay unlisted under --no-local: a fallback that regressed
// to the working directory would resolve the global layer to it instead of
// the empty text.
func TestConfigHomeRungFallbackHint(t *testing.T) {
	s := newSandbox(t)
	s.dropEnv("XDG_CONFIG_HOME", "HOME", "USERPROFILE")
	s.writeLocal(decoyYAML)
	assertListed(t, s.run("list", "-g", "--no-local"), emptyListText, "decoy")
}
