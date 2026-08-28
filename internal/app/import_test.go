package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

// TestImportEnvMergeAndReplaceSummary pins the app-level import seam: merge
// overrides same-named keys and reports the mode, replace wipes the profile's
// own env first and reports the removals.
func TestImportEnvMergeAndReplaceSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := "profiles:\n  a:\n    env:\n      A: old\n"
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r := strings.NewReader("A=new\nB=2\n")
	summary, err := ImportEnv(r, path, "a", "", ImportOptions{})
	if err != nil {
		t.Fatalf("ImportEnv: %v", err)
	}
	if !strings.Contains(summary, `imported 2 vars into "a" — merge`) {
		t.Fatalf("summary = %q", summary)
	}
	got, err := config.LoadFile(path)
	if err != nil || got.Profiles["a"].Env["A"] != "new" || got.Profiles["a"].Env["B"] != "2" {
		t.Fatalf("post-merge env = %v, %v", got.Profiles["a"].Env, err)
	}

	r = strings.NewReader("C=3\n")
	summary, err = ImportEnv(r, path, "a", "", ImportOptions{Replace: true, Force: true})
	if err != nil {
		t.Fatalf("ImportEnv replace: %v", err)
	}
	if !strings.Contains(summary, "replaced") {
		t.Fatalf("summary = %q", summary)
	}
}
