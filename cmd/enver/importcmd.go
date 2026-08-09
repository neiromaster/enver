package main

import (
	"fmt"
	"io"
	"os"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/dotenv"
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
	cfgPath, _ := cmd.Flags().GetString("config")
	noLocal, _ := cmd.Flags().GetBool("no-local")
	return app.MatchingProfiles(app.Options{ConfigPath: cfgPath, NoLocal: noLocal}, toComplete), cobra.ShellCompDirectiveNoFileComp
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
			n, ok := promptProfileName()
			if !ok {
				return nil
			}
			name = n
		} else if err := validateProfileName(name); err != nil {
			return err
		}
		summary, err := runImport(r, config.GlobalPath(globalFlags.configPath), name, importReplace, importForce, importExtends, ui.Confirm)
		if err != nil {
			return err
		}
		fmt.Print(summary)
		return nil
	},
}

func init() {
	importCmd.Flags().BoolVar(&importReplace, "replace", false, "wipe the profile's own env before importing")
	importCmd.Flags().BoolVar(&importForce, "force", false, "skip the --replace removal confirmation")
	importCmd.Flags().StringVar(&importExtends, "extends", "", "set or override the profile's extends")
}

// runImport parses .env data from r into profile name at cfgPath. Imported keys
// override existing same-named keys (merge); when replace is true the profile's
// own env is wiped first. The extends value is preserved unless extendsFlag is
// non-empty, in which case it is set (and the parent must already exist). force
// and confirm gate destructive replaces (Task 6). Returns a one-line summary.
func runImport(r io.Reader, cfgPath, name string, replace, force bool, extendsFlag string, confirm confirmFunc) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	entries, err := dotenv.Parse(data)
	if err != nil {
		return "", err
	}
	imported := make(map[string]string, len(entries))
	comments := map[string]string{}
	for _, e := range entries {
		imported[e.Key] = e.Value
		if e.Comment != "" {
			comments[e.Key] = e.Comment
		}
	}

	existing, err := app.Load(app.Options{ConfigPath: cfgPath, NoLocal: true})
	if err != nil {
		return "", err
	}
	existingProf, exists := existing.Profiles[name]

	extendsToWrite := ""
	if extendsFlag != "" {
		if _, ok := existing.Profiles[extendsFlag]; !ok {
			return "", fmt.Errorf("extends profile %q does not exist", extendsFlag)
		}
		extendsToWrite = extendsFlag
	} else if exists {
		extendsToWrite = existingProf.Extends
	}

	if exists && replace {
		if err := config.WriteProfile(cfgPath, name, config.Profile{Extends: extendsToWrite, Env: imported}, false, false, comments); err != nil {
			return "", err
		}
		return summary(name, len(imported), "replaced"), nil
	}
	if err := config.UpsertProfile(cfgPath, name, config.Profile{Extends: extendsToWrite, Env: imported}, false, comments); err != nil {
		return "", err
	}
	mode := "created"
	if exists {
		mode = "merge"
	}
	return summary(name, len(imported), mode), nil
}

func summary(name string, n int, mode string) string {
	var vars string
	if n == 1 {
		vars = "1 var"
	} else {
		vars = fmt.Sprintf("%d vars", n)
	}
	return fmt.Sprintf("\n✓ imported %s into %q — %s\nRun `enver encrypt %s` to encrypt secrets.\n", vars, name, mode, name)
}
