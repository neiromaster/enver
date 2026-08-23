package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

// TestCompleteProfileAppliesChdir pins that enverx --chdir reaches shell
// completion: the runner's PersistentPreRunE does not run during __complete, so
// the completion function must chdir itself.
func TestCompleteProfileAppliesChdir(t *testing.T) {
	saved := flags
	t.Cleanup(func() { flags = saved })

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	proj, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProfile(filepath.Join(proj, config.LocalFilename), "prod", config.Profile{Env: map[string]string{"K": "v"}}, false, false, nil); err != nil {
		t.Fatal(err)
	}

	// Neutral cwd with no local config and an empty global layer; --chdir points
	// at the project.
	neutral, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(neutral); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(neutral, "xdg"))
	flags.chdir = proj

	cmd := &cobra.Command{}
	cmd.Flags().String("config", "", "")
	cmd.Flags().Bool("no-local", false, "")

	got, directive := completeProfile(cmd, nil, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("directive = %v, want NoFileComp", directive)
	}
	if len(got) != 1 || got[0] != "prod" {
		t.Fatalf("completion = %v, want [prod] (from --chdir target)", got)
	}
}
