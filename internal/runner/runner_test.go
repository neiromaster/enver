package runner

import (
	"os"
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
	for i := 1; i < len(env); i++ {
		if env[i-1] > env[i] {
			t.Fatalf("env not sorted: %q before %q", env[i-1], env[i])
		}
	}
}

func TestMergedEnvDoesNotMutateOsEnviron(t *testing.T) {
	t.Setenv("ENVER_TEST_KEEP", "shell")
	_ = MergedEnv(map[string]string{"ENVER_TEST_KEEP": "profile"})
	if v, ok := os.LookupEnv("ENVER_TEST_KEEP"); !ok || v != "shell" {
		t.Fatalf("os.Environ mutated: got %q", v)
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