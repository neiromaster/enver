package main

import (
	"fmt"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

var defaultClear bool

var defaultCmd = &cobra.Command{
	Use:               "default [profile]",
	Short:             "Set or show the default profile",
	Args:              cobra.MaximumNArgs(1),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfileInTarget,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := writeTarget()
		if defaultClear {
			targetCfg, err := config.LoadFile(path)
			if err != nil {
				return err
			}
			if targetCfg.Default == "" {
				fmt.Println("(no default set in this file)")
				return nil
			}
			if err := config.ClearDefault(path); err != nil {
				return err
			}
			fmt.Println("✓ cleared default profile")
			return nil
		}
		cfg, err := app.Load(appOpts())
		if err != nil {
			return err
		}
		if len(args) == 0 {
			if cfg.Default == "" {
				return fmt.Errorf("no default profile set")
			}
			fmt.Println(cfg.Default)
			return nil
		}
		name := args[0]
		if err := validateProfileName(name); err != nil {
			return err
		}
		targetCfg, err := config.LoadFile(path)
		if err != nil {
			return err
		}
		if _, ok := targetCfg.Profiles[name]; !ok {
			return notFoundInTarget(name, path)
		}
		if err := config.SetDefault(path, name); err != nil {
			return err
		}
		fmt.Printf("✓ default profile set to %q\n", name)
		return nil
	},
}

func init() {
	defaultCmd.Flags().BoolVar(&defaultClear, "clear", false, "clear the default profile")
}
