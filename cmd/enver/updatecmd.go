package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/neiromaster/enver/internal/update"
	"github.com/neiromaster/enver/internal/version"
	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update enver to the latest release",
	Long: `Update enver to the latest release.

The update is applied by the package manager that owns the install (npm,
brew, go install) or, for a directly-installed binary, by downloading the
release and replacing the binary in place.

  enver update            apply the latest release
  enver update --check    only report whether an update is available

Exit codes: 0 up to date, 1 update available (--check), 2 error.`,
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		check, _ := cmd.Flags().GetBool("check")
		return runUpdate(check)
	},
}

func init() {
	updateCmd.Flags().Bool("check", false, "report whether an update is available and exit, without applying it")
}

// execPath resolves the running binary to the real file (symlinks unwound), so
// a symlinked enver updates the target, not the link. Overridable in tests.
var execPath = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

// delegate runs the owning package manager's update with stdio connected.
// Overridable in tests.
var delegate = func(name string, args []string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s update failed: %w", name, err)
	}
	return nil
}

// runUpdate applies the update, or reports it under --check. The returned
// codedError carries the exit code out of main (0 nil, 1 available, 2 error).
func runUpdate(check bool) error {
	exe, err := execPath()
	if err != nil {
		return exitErr(fmt.Errorf("locate the enver binary: %w", err), 2)
	}
	c := update.NewClient(update.DefaultAPIBase, update.DefaultDownloadBase)
	latest, err := c.LatestVersion()
	if err != nil {
		return exitErr(fmt.Errorf("check for the latest release: %w", err), 2)
	}
	current := version.Version
	if update.LatestOrEqual(current, latest) {
		fmt.Printf("enver %s is up to date\n", current)
		return nil
	}
	if check {
		fmt.Printf("an update is available: %s -> %s\n", current, latest)
		return &codedError{err: fmt.Errorf("update available"), code: 1, silent: true}
	}
	if err := applyUpdate(c, exe, latest); err != nil {
		return exitErr(err, 2)
	}
	return nil
}

// applyUpdate delegates to the owning package manager or self-replaces.
func applyUpdate(c *update.Client, exe, latest string) error {
	m := update.ResolveMethod(exe)
	if name, args, ok := m.Command(exe); ok {
		return delegate(name, args)
	}
	if err := update.SelfReplace(c, exe, latest); err != nil {
		return err
	}
	fmt.Printf("enver updated to %s (takes effect on the next launch)\n", latest)
	return nil
}
