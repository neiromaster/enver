package runner

import (
	"os"
	"os/exec"
	"testing"
)

func TestMergedEnvOverrides(t *testing.T) {
	t.Setenv("ENVER_TEST_EXISTING", "from-shell")
	t.Setenv("ENVER_TEST_SHARED", "from-shell")

	env := MergedEnv(map[string]string{
		"ENVER_TEST_SHARED":  "from-profile",
		"ENVER_TEST_PROFILE": "from-profile",
	})

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
	t.Setenv("ZZZ_TEST", "1")
	t.Setenv("AAA_TEST", "1")
	env := MergedEnv(map[string]string{"MMM_TEST": "1"})
	// Compare keys, not full KEY=VALUE entries: values may contain bytes (e.g.
	// "(x86)" in Windows path env vars) that sort differently from keys.
	prev := ""
	for _, kv := range env {
		key, _, ok := splitKV(kv)
		if !ok {
			t.Fatalf("entry missing '=': %q", kv)
		}
		if prev > key {
			t.Fatalf("env keys not sorted: %q before %q", prev, key)
		}
		prev = key
	}
}

func TestMergedEnvDoesNotMutateOsEnviron(t *testing.T) {
	t.Setenv("ENVER_TEST_KEEP", "shell")
	_ = MergedEnv(map[string]string{"ENVER_TEST_KEEP": "profile"})
	if v, ok := os.LookupEnv("ENVER_TEST_KEEP"); !ok || v != "shell" {
		t.Fatalf("os.Environ mutated: got %q", v)
	}
}

func TestRunMissingCommandReturns127(t *testing.T) {
	missing := "enver-definitely-not-a-real-command-xyz"
	if _, err := exec.LookPath(missing); err == nil {
		t.Fatalf("precondition failed: %q unexpectedly resolves on PATH", missing)
	}
	code := Run([]string{missing}, MergedEnv(nil), "enver x", "p")
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
