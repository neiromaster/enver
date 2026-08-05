// Command enverx runs a child command with an enver profile's environment
// injected. It is the detached, runner-only companion to enver's `x` subcommand.
package main

import (
	"fmt"
	"os"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/version"
	"github.com/spf13/cobra"
)

var flags struct {
	configPath string
	keyPath    string
	noLocal    bool
}

var rootCmd = &cobra.Command{
	Use:               "enverx [profile] -- <command> [args...]",
	Short:             "Run a command with an enver profile's environment injected",
	Args:              cobra.ArbitraryArgs,
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfile,
	Version:           version.String(),
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := app.Options{
			ConfigPath: flags.configPath,
			KeyPath:    flags.keyPath,
			NoLocal:    flags.noLocal,
			Name:       "enverx",
		}
		return app.Run(args, cmd.ArgsLenAtDash(), opts)
	},
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flags.configPath, "config", "", "override the global config file")
	pf.StringVar(&flags.keyPath, "key", "", "key file (or ENVER_KEY env)")
	pf.BoolVar(&flags.noLocal, "no-local", false, "ignore .enver.yaml layers")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "enverx: %v\n", err)
		os.Exit(1)
	}
}

func completeProfile(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	cfgPath, _ := cmd.Flags().GetString("config")
	noLocal, _ := cmd.Flags().GetBool("no-local")
	return app.MatchingProfiles(app.Options{ConfigPath: cfgPath, NoLocal: noLocal}, toComplete), cobra.ShellCompDirectiveNoFileComp
}
