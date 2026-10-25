package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

// completeImport drives shell completion for `enver import`: the first positional
// argument is a .env file path (default file completion), the second is a profile
// name (profile completion). This inverts completeProfile, which assumes arg 0 is
// a profile and is wrong for import.
func completeImport(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	return targetProfiles(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
}

var (
	importReplace bool
	importForce   bool
	importExtends string
)

var importCmd = &cobra.Command{
	Use:               "import <file> [profile]",
	Short:             "Import a .env file into a profile",
	Args:              cobra.RangeArgs(1, 2),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeImport,
	RunE: func(cmd *cobra.Command, args []string) error {
		file := args[0]
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		var r io.Reader
		if file == "-" {
			r = os.Stdin
		} else {
			f, err := os.Open(file)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			r = f
		}
		if name == "" {
			if err := requireInteractive("profile name"); err != nil {
				return err
			}
			n, ok := promptProfileName()
			if !ok {
				return nil
			}
			name = n
		} else if err := validateProfileName(name); err != nil {
			return err
		}
		if importExtends != "" {
			parents := app.SplitExtends(importExtends)
			// Parents and cycles are judged on the merged view: resolution
			// spans both layers, so a global pick can loop through a local
			// parent and vice versa.
			merged, err := app.Load(appOpts())
			if err != nil {
				return err
			}
			for _, p := range parents {
				if _, ok := merged.Profiles[p]; !ok {
					return fmt.Errorf("extends profile %q does not exist", p)
				}
			}
			if err := extendsCycleError(merged, name, parents); err != nil {
				return err
			}
		}
		summary, err := app.ImportEnv(r, writeTarget(), name, importExtends, app.ImportOptions{
			Replace: importReplace,
			Force:   importForce,
			Confirm: ui.Confirm,
			Resolve: effectiveResolve,
		})
		if err != nil {
			return err
		}
		if summary != "" {
			fmt.Print(summary)
			return nil
		}
		return aborted(cmd.OutOrStdout())
	},
}

func init() {
	importCmd.Flags().BoolVar(&importReplace, "replace", false, "wipe the profile's own env and unset list before importing")
	importCmd.Flags().BoolVar(&importForce, "force", false, "skip the --replace removal confirmation")
	importCmd.Flags().StringVar(&importExtends, "extends", "", "set or override the profile's extends (comma-separated for multiple)")
	_ = importCmd.RegisterFlagCompletionFunc("extends", completeImportExtends)
}

// completeImportExtends completes the --extends flag: the trailing
// comma-segment against the merged view the flag is itself validated on,
// keeping typed parents as the prefix and not re-offering them.
func completeImportExtends(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if err := applyChdir(); err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	prefix := ""
	if i := strings.LastIndex(toComplete, ","); i >= 0 {
		prefix = toComplete[:i+1]
		toComplete = toComplete[i+1:]
	}
	picked := app.SplitExtends(prefix)
	cfgPath, _ := cmd.Flags().GetString("config")
	noLocal, _ := cmd.Flags().GetBool("no-local")
	var out []string
	for _, n := range app.MatchingProfiles(app.Options{ConfigPath: cfgPath, NoLocal: noLocal}, toComplete) {
		if !slices.Contains(picked, n) {
			out = append(out, prefix+n)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// effectiveResolve resolves a profile from the merged two-layer view the
// read commands use, after import has written its target file — fence
// reporting must judge the profile as show, export, and x will see it.
func effectiveResolve(profile string) (config.Resolved, error) {
	cfg, err := app.Load(appOpts())
	if err != nil {
		return config.Resolved{}, err
	}
	return cfg.ResolveProfile(profile)
}
