package runner

import (
	"os/exec"
	"reflect"
	"runtime"
	"strconv"
	"testing"
)

func TestMergedEnvOverrides(t *testing.T) {
	env := MergedEnv(
		map[string]string{
			"ENVER_TEST_EXISTING": "from-shell",
			"ENVER_TEST_SHARED":   "from-shell",
		},
		map[string]string{
			"ENVER_TEST_SHARED":  "from-profile",
			"ENVER_TEST_PROFILE": "from-profile",
		},
	)

	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := splitKV(kv)
		m[k] = v
	}
	if m["ENVER_TEST_EXISTING"] != "from-shell" {
		t.Errorf("existing env var overwritten: %q", m["ENVER_TEST_EXISTING"])
	}
	if m["ENVER_TEST_SHARED"] != "from-profile" {
		t.Errorf("shared var not overridden by profile: %q", m["ENVER_TEST_SHARED"])
	}
	if m["ENVER_TEST_PROFILE"] != "from-profile" {
		t.Errorf("profile-only var missing: %q", m["ENVER_TEST_PROFILE"])
	}
}

func TestMergedEnvSorted(t *testing.T) {
	env := MergedEnv(
		map[string]string{"ZZZ_TEST": "1", "AAA_TEST": "1"},
		map[string]string{"MMM_TEST": "1"},
	)
	want := []string{"AAA_TEST=1", "MMM_TEST=1", "ZZZ_TEST=1"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("merged env = %v, want %v", env, want)
	}
}

func TestMergedEnvDoesNotMutateInputs(t *testing.T) {
	osEnv := map[string]string{"ENVER_TEST_KEEP": "shell"}
	profileEnv := map[string]string{"ENVER_TEST_OVERRIDE": "profile"}
	_ = MergedEnv(osEnv, profileEnv)
	if osEnv["ENVER_TEST_KEEP"] != "shell" {
		t.Fatalf("osEnv mutated: got %q", osEnv["ENVER_TEST_KEEP"])
	}
	if profileEnv["ENVER_TEST_OVERRIDE"] != "profile" {
		t.Fatalf("profileEnv mutated: got %q", profileEnv["ENVER_TEST_OVERRIDE"])
	}
}

func TestRunMissingCommandReturns127(t *testing.T) {
	missing := "enver-definitely-not-a-real-command-xyz"
	if _, err := exec.LookPath(missing); err == nil {
		t.Fatalf("precondition failed: %q unexpectedly resolves on PATH", missing)
	}
	code := Run([]string{missing}, MergedEnv(nil, nil), "enver x", "p")
	if code != 127 {
		t.Fatalf("expected exit code 127 for missing command, got %d", code)
	}
}

func TestLaunchTitleOSC(t *testing.T) {
	got := launchTitleOSC("anth")
	want := "\x1b]0;claude code\x07\x1b]0;anth\x07"
	if got != want {
		t.Fatalf("launchTitleOSC = %q, want %q", got, want)
	}
}

func splitKV(kv string) (string, string, bool) {
	for i, c := range kv {
		if c == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return kv, "", false
}

func TestMergedEnvCaseVariantOverlay(t *testing.T) {
	got := MergedEnv(map[string]string{"PATH": "shell"}, map[string]string{"Path": "profile"})
	if runtime.GOOS == "windows" {
		if len(got) != 1 || got[0] != "Path=profile" {
			t.Fatalf("MergedEnv = %v, want [Path=profile] — the profile variant replaces the shell one", got)
		}
	} else if len(got) != 2 {
		t.Fatalf("MergedEnv = %v, want both spellings on POSIX", got)
	}
}

// childExitCommand returns a child that exits with the given code: cmd on
// windows, sh on posix.
func childExitCommand(t *testing.T, code int) (string, []string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		path, err := exec.LookPath("cmd")
		if err != nil {
			t.Fatalf("cmd.exe not on PATH: %v", err)
		}
		return path, []string{"cmd", "/c", "exit", strconv.Itoa(code)}
	}
	path, err := exec.LookPath("sh")
	if err != nil {
		t.Fatalf("sh not on PATH: %v", err)
	}
	return path, []string{"sh", "-c", "exit " + strconv.Itoa(code)}
}

// TestExecChildPropagatesExitCode pins the child-wait path: the child's exit
// code is ours. Windows always waits; on unix the wait path is coverage-only,
// so the test stands in for a coverage build with a non-empty GOCOVERDIR —
// the default execve path would replace the test process itself.
func TestExecChildPropagatesExitCode(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Setenv("GOCOVERDIR", t.TempDir())
	}
	path, args := childExitCommand(t, 3)
	if code := execChild(path, args, nil, "enver x"); code != 3 {
		t.Fatalf("execChild exit = %d, want 3", code)
	}
	path, args = childExitCommand(t, 0)
	if code := execChild(path, args, nil, "enver x"); code != 0 {
		t.Fatalf("execChild exit = %d, want 0", code)
	}
}

// TestExecChildSpawnFailureReturnsOne pins the non-ExitError path: a spawn
// that never starts reports 1.
func TestExecChildSpawnFailureReturnsOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		// A directory as Path is rejected by os/exec before any child starts, a non-ExitError.
		dir := t.TempDir()
		if code := execChild(dir, []string{dir}, nil, "enver x"); code != 1 {
			t.Fatalf("execChild(directory) = %d, want 1", code)
		}
		return
	}
	// syscall.Exec failure path: the process survives a bad path. Clear
	// GOCOVERDIR to pin the execve branch — a coverage build sets it and
	// would take the wait path instead.
	t.Setenv("GOCOVERDIR", "")
	bad := "/enver-no-such-binary-xyz"
	if code := execChild(bad, []string{bad}, nil, "enver x"); code != 1 {
		t.Fatalf("execChild(bad path) = %d, want 1", code)
	}
}
