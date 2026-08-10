package main

import (
	"fmt"
	"strings"

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
	ValidArgsFunction: completeProfile,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := app.Load(appOpts())
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
			picked, err := pickProfile(cfg, "Profile to remove", "")
			if err != nil || picked == "" {
				return nil
			}
			name = picked
		}
		if err := guardRemovable(cfg, name); err != nil {
			return err
		}
		if !removeYes {
			if !ui.Interactive() {
				return fmt.Errorf("non-interactive; pass --yes to confirm removal")
			}
			ans, err := ui.Confirm(fmt.Sprintf("Delete profile %q?", name), false)
			if err != nil || !ans {
				fmt.Println("\naborted")
				return nil
			}
		}
		path := config.GlobalPath(globalFlags.configPath)
		if err := config.DeleteProfile(path, name); err != nil {
			return err
		}
		fmt.Printf("\n✓ removed profile %q\n", name)
		return nil
	},
}

// guardRemovable refuses to delete a profile that other profiles extend or that is
// the current default, naming the dependents.
func guardRemovable(cfg config.Config, name string) error {
	if _, ok := cfg.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	var blocks []string
	if extendedBy := cfg.ExtendedBy(name); len(extendedBy) > 0 {
		blocks = append(blocks, fmt.Sprintf("extended by %v", extendedBy))
	}
	if cfg.Default == name {
		blocks = append(blocks, "is the default profile")
	}
	if len(blocks) > 0 {
		return fmt.Errorf("refusing to remove %q: %s; repoint or remove those first", name, strings.Join(blocks, "; "))
	}
	return nil
}

func init() {
	removeCmd.Flags().BoolVarP(&removeYes, "yes", "y", false, "skip confirmation")
}
