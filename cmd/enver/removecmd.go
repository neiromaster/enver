package main

import (
	"fmt"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

var removeYes bool

var removeCmd = &cobra.Command{
	Use:               "remove [profile]",
	Short:             "Delete a profile",
	Args:              cobra.MaximumNArgs(1),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfileInTarget,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := writeTarget()
		targetCfg, err := config.LoadFile(path)
		if err != nil {
			return err
		}
		name := ""
		if len(args) > 0 {
			name = args[0]
		}
		if name == "" {
			if err := requireInteractive("profile name"); err != nil {
				return err
			}
			picked, err := pickProfile(targetCfg, "Profile to remove", "")
			if err != nil || picked == "" {
				return nil
			}
			name = picked
		}
		if _, ok := targetCfg.Profiles[name]; !ok {
			return notFoundInTarget(name, path)
		}
		merged, err := app.Load(appOpts())
		if err != nil {
			return err
		}
		if err := guardRemovable(merged, name, "remove"); err != nil {
			return err
		}
		if !removeYes {
			if !ui.Interactive() {
				return fmt.Errorf("non-interactive; pass --yes to confirm removal")
			}
			ans, err := ui.Confirm(fmt.Sprintf("Delete profile %q?", name), false)
			if err != nil || !ans {
				return aborted(cmd.OutOrStdout())
			}
		}
		clearedDefault := targetCfg.Default == name
		if err := config.DeleteProfile(path, name); err != nil {
			return err
		}
		if clearedDefault {
			fmt.Printf("\n✓ removed profile %q (default cleared)\n", name)
		} else {
			fmt.Printf("\n✓ removed profile %q\n", name)
		}
		return nil
	},
}

// guardRemovable blocks deletion when other profiles extend name (merged view —
// a local child can depend on a global parent). Deleting the file's default is
// allowed: DeleteProfile clears the default key along with the profile. Callers
// pre-check existence so DeleteProfile's missing-file no-op cannot mask a wrong
// scope. verb names the refused action ("remove", "rename") in the error.
func guardRemovable(merged config.Config, name, verb string) error {
	if extendedBy := merged.ExtendedBy(name); len(extendedBy) > 0 {
		return fmt.Errorf("refusing to %s %q: extended by %v; repoint or remove those first", verb, name, extendedBy)
	}
	return nil
}

func init() {
	removeCmd.Flags().BoolVarP(&removeYes, "yes", "y", false, "skip confirmation")
}
