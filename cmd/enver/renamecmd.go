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
	ValidArgsFunction: completeProfile,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := app.Load(appOpts())
		if err != nil {
			return err
		}
		oldName := ""
		if len(args) > 0 {
			oldName = args[0]
		}
		if oldName == "" {
			picked, err := pickProfile(cfg, "Profile to rename", "")
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
			in, err := ui.Input("New name")
			if err != nil {
				return nil
			}
			newName = strings.TrimSpace(in)
		}
		if err := validateProfileName(newName); err != nil {
			return err
		}
		if newName != oldName {
			if _, ok := cfg.Profiles[newName]; ok {
				return fmt.Errorf("profile %q already exists", newName)
			}
		}
		path := config.GlobalPath(globalFlags.configPath)
		if err := config.RenameProfile(path, oldName, newName); err != nil {
			return err
		}
		fmt.Printf("✓ renamed %q → %q\n", oldName, newName)
		return nil
	},
}
