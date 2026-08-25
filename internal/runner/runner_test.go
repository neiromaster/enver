package runner

import (
	"os/exec"
	"reflect"
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
		nil,
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
		nil,
	)
	want := []string{"AAA_TEST=1", "MMM_TEST=1", "ZZZ_TEST=1"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("merged env = %v, want %v", env, want)
	}
}

func TestMergedEnvDoesNotMutateInputs(t *testing.T) {
	osEnv := map[string]string{"ENVER_TEST_KEEP": "shell"}
	profileEnv := map[string]string{"ENVER_TEST_OVERRIDE": "profile"}
	_ = MergedEnv(osEnv, profileEnv, nil)
	if osEnv["ENVER_TEST_KEEP"] != "shell" {
		t.Fatalf("osEnv mutated: got %q", osEnv["ENVER_TEST_KEEP"])
	}
	if profileEnv["ENVER_TEST_OVERRIDE"] != "profile" {
		t.Fatalf("profileEnv mutated: got %q", profileEnv["ENVER_TEST_OVERRIDE"])
	}
}

func TestMergedEnvUnset(t *testing.T) {
	// An unset removes the key even when the shell exports it and the profile
	// redefines it — both contributions must be dropped.
	env := MergedEnv(
		map[string]string{
			"ENVER_TEST_UNSET_SHELL":  "from-shell",
			"ENVER_TEST_UNSET_BOTH":   "from-shell",
			"ENVER_TEST_KEEP":         "from-shell",
			"ENVER_TEST_PROFILE_REAL": "shell",
		},
		map[string]string{
			"ENVER_TEST_UNSET_BOTH":    "from-profile",
			"ENVER_TEST_UNSET_PROFILE": "from-profile",
			"ENVER_TEST_PROFILE_REAL":  "profile",
		},
		[]string{"ENVER_TEST_UNSET_SHELL", "ENVER_TEST_UNSET_BOTH", "ENVER_TEST_UNSET_PROFILE"},
	)

	m := map[string]string{}
	for _, kv := range env {
		k, v, _ := splitKV(kv)
		m[k] = v
	}
	for _, gone := range []string{"ENVER_TEST_UNSET_SHELL", "ENVER_TEST_UNSET_BOTH", "ENVER_TEST_UNSET_PROFILE"} {
		if _, ok := m[gone]; ok {
			t.Errorf("unset key %q leaked into merged env: %v", gone, env)
		}
	}
	if m["ENVER_TEST_KEEP"] != "from-shell" {
		t.Errorf("non-unset shell key dropped: %q", m["ENVER_TEST_KEEP"])
	}
	if m["ENVER_TEST_PROFILE_REAL"] != "profile" {
		t.Errorf("non-unset shared key not overridden by profile: %q", m["ENVER_TEST_PROFILE_REAL"])
	}
}

func TestRunMissingCommandReturns127(t *testing.T) {
	missing := "enver-definitely-not-a-real-command-xyz"
	if _, err := exec.LookPath(missing); err == nil {
		t.Fatalf("precondition failed: %q unexpectedly resolves on PATH", missing)
	}
	code := Run([]string{missing}, MergedEnv(nil, nil, nil), "enver x", "p")
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
