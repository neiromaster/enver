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
		if err := guardRemovable(merged, targetCfg, name); err != nil {
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
		if err := config.DeleteProfile(path, name); err != nil {
			return err
		}
		fmt.Printf("\n✓ removed profile %q\n", name)
		return nil
	},
}

// guardRemovable refuses to delete a profile that other profiles extend or that
// is the target file's default. ExtendedBy is checked against the merged config
// (a local child can depend on a global parent); the default check is against
// the target file, since that is the file being mutated. Existence is assumed —
// callers check the target file first so DeleteProfile's missing-file no-op
// cannot mask a wrong scope.
func guardRemovable(merged, target config.Config, name string) error {
	var blocks []string
	if extendedBy := merged.ExtendedBy(name); len(extendedBy) > 0 {
		blocks = append(blocks, fmt.Sprintf("extended by %v", extendedBy))
	}
	if target.Default == name {
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
