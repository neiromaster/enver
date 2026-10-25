package main

import (
	"fmt"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

var duplicateCmd = &cobra.Command{
	Use:               "duplicate <src> [new]",
	Short:             "Copy a profile (with its extends, env, and comments)",
	Args:              cobra.MaximumNArgs(2),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfileInTarget,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, targetCfg, err := loadTarget()
		if err != nil {
			return err
		}
		src := ""
		if len(args) > 0 {
			src = args[0]
		}
		if src == "" {
			if err := requireInteractive("profile name"); err != nil {
				return err
			}
			picked, err := pickProfile(targetCfg, "Profile to duplicate", "")
			if err != nil || picked == "" {
				return nil
			}
			src = picked
		}
		newName := ""
		if len(args) > 1 {
			newName = args[1]
			if err := validateProfileName(newName); err != nil {
				return err
			}
		} else {
			if err := requireInteractive("new name"); err != nil {
				return err
			}
			n, ok := promptProfileName("New profile name", ui.Input)
			if !ok {
				return nil
			}
			newName = n
		}
		if _, ok := targetCfg.Profiles[newName]; ok {
			return fmt.Errorf("profile %q already exists", newName)
		}
		prof, _, ok, err := config.ReadProfile(path, src)
		if err != nil {
			return err
		}
		if !ok {
			return notFoundInTarget(src, path)
		}
		if err := config.UpsertProfile(path, newName, prof, false, true); err != nil {
			return err
		}
		fmt.Printf("\n✓ duplicated %q → %q\n", src, newName)
		return nil
	},
}
