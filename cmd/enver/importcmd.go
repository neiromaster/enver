package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/dotenv"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

var importReplace bool

var importCmd = &cobra.Command{
	Use:               "import <file> [profile]",
	Short:             "Import a .env file into a profile",
	Args:              cobra.RangeArgs(1, 2),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfile,
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
			for {
				n, err := ui.Input("Profile name")
				if err != nil {
					return nil
				}
				n = strings.TrimSpace(n)
				if err := validateProfileName(n); err != nil {
					fmt.Println("  invalid: use letters, digits, '-' or '_'; must start with a letter or digit")
					continue
				}
				name = n
				break
			}
		} else if err := validateProfileName(name); err != nil {
			return err
		}
		summary, err := runImport(r, config.GlobalPath(globalFlags.configPath), name, importReplace)
		if err != nil {
			return err
		}
		fmt.Print(summary)
		return nil
	},
}

func init() {
	importCmd.Flags().BoolVar(&importReplace, "replace", false, "wipe the profile's own env before importing")
}

// runImport parses .env data from r into profile name at cfgPath. When the profile
// exists it merges (imported keys override, others kept) unless replace is true,
// in which case it is overwritten wholesale. Returns a one-line summary.
func runImport(r io.Reader, cfgPath, name string, replace bool) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	entries, err := dotenv.Parse(data)
	if err != nil {
		return "", err
	}
	env := make(map[string]string, len(entries))
	comments := map[string]string{}
	for _, e := range entries {
		env[e.Key] = e.Value
		if e.Comment != "" {
			comments[e.Key] = e.Comment
		}
	}

	existing, _ := app.Load(app.Options{ConfigPath: cfgPath, NoLocal: true})
	_, exists := existing.Profiles[name]
	mode := "created"
	if exists {
		mode = "merge"
		if replace {
			mode = "replaced"
			if err := config.WriteProfile(cfgPath, name, config.Profile{Env: env}, false, false, comments); err != nil {
				return "", err
			}
			return summary(name, len(entries), mode), nil
		}
	}
	if err := config.UpsertProfile(cfgPath, name, config.Profile{Env: env}, false, comments); err != nil {
		return "", err
	}
	return summary(name, len(entries), mode), nil
}

func summary(name string, n int, mode string) string {
	return fmt.Sprintf("\n✓ imported %d var(s) into %q — %s\nRun `enver encrypt %s` to encrypt secrets.\n", n, name, mode, name)
}
