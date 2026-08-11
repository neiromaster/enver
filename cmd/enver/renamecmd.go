package main

import (
	"fmt"
	"strings"

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
	ValidArgsFunction: completeProfile,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := writeTarget()
		targetCfg, err := config.LoadFile(path)
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
		if _, ok := targetCfg.Profiles[oldName]; !ok {
			return notFoundInTarget(oldName, path)
		}
		if newName != oldName {
			if _, ok := targetCfg.Profiles[newName]; ok {
				return fmt.Errorf("profile %q already exists", newName)
			}
		}
		if newName == oldName {
			fmt.Printf("%q is already named that; nothing to rename\n", newName)
			return nil
		}
		if err := config.RenameProfile(path, oldName, newName); err != nil {
			return err
		}
		fmt.Printf("\n✓ renamed %q → %q\n", oldName, newName)
		return nil
	},
}
