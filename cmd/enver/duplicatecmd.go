package main

import (
	"fmt"
	"strings"

	"github.com/neiromaster/enver/internal/app"
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
	ValidArgsFunction: completeProfile,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := app.Load(appOpts())
		if err != nil {
			return err
		}
		src := ""
		if len(args) > 0 {
			src = args[0]
		}
		if src == "" {
			picked, err := pickProfile(cfg, "Profile to duplicate", "")
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
			in, err := ui.Input("New profile name")
			if err != nil {
				return nil
			}
			newName = strings.TrimSpace(in)
		}
		if err := validateProfileName(newName); err != nil {
			return err
		}
		if _, ok := cfg.Profiles[newName]; ok {
			return fmt.Errorf("profile %q already exists", newName)
		}
		path := config.GlobalPath(globalFlags.configPath)
		prof, comments, _, ok, err := config.ReadProfile(path, src)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("profile %q not found", src)
		}
		if err := config.UpsertProfile(path, newName, prof, false, comments); err != nil {
			return err
		}
		fmt.Printf("✓ duplicated %q → %q\n", src, newName)
		return nil
	},
}
