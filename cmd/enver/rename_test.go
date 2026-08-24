package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neiromaster/enver/internal/config"
)

// captureStdout swaps os.Stdout for a pipe, runs fn, then restores it and
// returns everything written. renameCmd prints via fmt.Printf against os.Stdout
// directly (not cmd.OutOrStdout), so a pipe is the only way to observe it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(out)
}

func TestRenameSameNameNoOp(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := config.UpsertProfile(cfgPath, "p", config.Profile{Env: map[string]string{"A": "1"}}, false, false); err != nil {
		t.Fatal(err)
	}
	withGlobalConfig(t, cfgPath)

	out := captureStdout(t, func() {
		if err := renameCmd.RunE(renameCmd, []string{"p", "p"}); err != nil {
			t.Fatalf("RunE: %v", err)
		}
	})

	if strings.Contains(out, "✓ renamed") {
		t.Errorf("no-op rename must not print success:\n%s", out)
	}
	if !strings.Contains(out, "nothing to rename") {
		t.Errorf("no-op rename should report nothing to rename:\n%s", out)
	}
	prof, _, _, ok, err := config.ReadProfile(cfgPath, "p")
	if err != nil || !ok {
		t.Fatalf("profile p missing after no-op: %v", err)
	}
	if prof.Env["A"] != "1" {
		t.Errorf("profile env changed on no-op: %+v", prof.Env)
	}
}
