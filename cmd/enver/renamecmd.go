package main

import (
	"fmt"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

var renameCmd = &cobra.Command{
	Use:               "rename [old] [new]",
	Short:             "Rename a profile and rewrite extends/default references",
	Args:              cobra.MaximumNArgs(2),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfileInTarget,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, targetCfg, err := loadTarget()
		if err != nil {
			return err
		}
		oldName := ""
		if len(args) > 0 {
			oldName = args[0]
		}
		if oldName == "" {
			if err := requireInteractive("profile name"); err != nil {
				return err
			}
			picked, err := pickProfile(targetCfg, "Profile to rename", "")
			if err != nil || picked == "" {
				return nil
			}
			oldName = picked
		}
		newName := ""
		if len(args) > 1 {
			newName = args[1]
		}
		if newName == "" {
			if err := requireInteractive("new name"); err != nil {
				return err
			}
			in, err := ui.Input("New name")
			if err != nil {
				return nil
			}
			newName = strings.TrimSpace(in)
		}
		if err := validateProfileName(newName); err != nil {
			return err
		}
		if err := requireTargetProfile(targetCfg, path, oldName); err != nil {
			return err
		}
		if newName == oldName {
			fmt.Printf("%q is already named that; nothing to rename\n", newName)
			return nil
		}
		// RenameProfile rewrites extends refs in the target file only, so a
		// profile extended from the other layer would dangle its children.
		merged, err := app.Load(appOpts())
		if err != nil {
			return err
		}
		if err := guardRemovable(merged, oldName, "rename"); err != nil {
			return err
		}
		if err := config.RenameProfile(path, oldName, newName); err != nil {
			return err
		}
		fmt.Printf("\n✓ renamed %q → %q\n", oldName, newName)
		return nil
	},
}
