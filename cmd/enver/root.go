package main

import (
	"fmt"
	"os"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/version"
	"github.com/spf13/cobra"
)

var globalFlags struct {
	configPath string
	keyPath    string
	noLocal    bool
}

var rootCmd = &cobra.Command{
	Use:   "enver",
	Short: "Inject environment variables from layered YAML profiles into a command",
	Long: `enver — environment profile injector.

Manage named, layered YAML profiles and run commands with a profile's
environment injected, without mutating any tool's own config.

  enver x <profile> -- <command>        run a command with a profile
  enverx <profile> -- <command>         dedicated runner binary (same thing)
  enver show <profile>                  preview resolved env (masked)
  enver export <profile>                print ` + "`export K=V`" + ` for eval
  enver list                            list profiles
  enver add [name]                      create a profile interactively
  enver keygen | encrypt | decrypt      manage encrypted secrets

Config: $XDG_CONFIG_HOME/enver/config.yaml (default ~/.config/enver/config.yaml),
layered with .enver.yaml walked from cwd up to $HOME.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.Version = version.String()

	pf := rootCmd.PersistentFlags()
	pf.StringVar(&globalFlags.configPath, "config", "", "override the global config file")
	pf.StringVar(&globalFlags.keyPath, "key", "", "key file (or ENVER_KEY env)")
	pf.BoolVar(&globalFlags.noLocal, "no-local", false, "ignore .enver.yaml layers")

	rootCmd.AddCommand(xCmd, showCmd, exportCmd, listCmd, keygenCmd, encryptCmd, decryptCmd, addCmd, defaultCmd, validateCmd, removeCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		os.Exit(1)
	}
}

func appOpts() app.Options {
	return app.Options{
		ConfigPath: globalFlags.configPath,
		KeyPath:    globalFlags.keyPath,
		NoLocal:    globalFlags.noLocal,
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
