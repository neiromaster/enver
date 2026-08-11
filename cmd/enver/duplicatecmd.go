package main

import (
	"fmt"
	"strings"

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
		path := writeTarget()
		targetCfg, err := config.LoadFile(path)
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
		}
		if newName == "" {
			if err := requireInteractive("new name"); err != nil {
				return err
			}
			in, err := ui.Input("New profile name")
			if err != nil {
				return nil
			}
			newName = strings.TrimSpace(in)
		}
		if err := validateProfileName(newName); err != nil {
			return err
		}
		if _, ok := targetCfg.Profiles[newName]; ok {
			return fmt.Errorf("profile %q already exists", newName)
		}
		prof, comments, _, ok, err := config.ReadProfile(path, src)
		if err != nil {
			return err
		}
		if !ok {
			return notFoundInTarget(src, path)
		}
		if err := config.UpsertProfile(path, newName, prof, false, true, comments); err != nil {
			return err
		}
		fmt.Printf("\n✓ duplicated %q → %q\n", src, newName)
		return nil
	},
}
