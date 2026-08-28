package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/neiromaster/enver/internal/version"
	"github.com/spf13/cobra"
)

var globalFlags struct {
	configPath string
	keyPath    string
	noLocal    bool
	noExpand   bool
	global     bool
	chdir      string
}

var rootCmd = &cobra.Command{
	Use:   "enver",
	Short: "Inject environment variables from layered YAML profiles into a command",
	Long: `enver — environment profile injector.

Manage named, layered YAML profiles and run commands with a profile's
environment injected, without mutating any tool's own config.

  enver x <profile> -- <command>        run a command with a profile
  enver show <profile>                  preview resolved env (masked)
  enver export <profile>                print ` + "`export K=V`" + ` for eval
  enver dotenv <profile>                export a profile to a .env file (with comments)
  enver import <file> [profile]         import a .env file into a profile
  enver list                            list profiles
  enver add [name]                      interactively add a profile
  enver edit [profile]                  edit a profile (vars, extends, default)
  enver remove [profile]                delete a profile
  enver rename [old] [new]              rename a profile (+ extends/default refs)
  enver duplicate <src> [new]           copy a profile
  enver default [profile]               set/show the default (--clear to clear)
  enver validate                        check config health
  enver keygen | encrypt | decrypt      manage encrypted secrets

Profile names may collide with subcommand verbs; enver x <name> -- <command>
addresses such a profile directly.

Config: $XDG_CONFIG_HOME/enver/config.yaml (default ~/.config/enver/config.yaml),
plus a local .enver.yaml in the current directory. Mutating commands write to
the local file by default; pass --global (-g) to write the user config.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.Version = version.String()

	pf := rootCmd.PersistentFlags()
	pf.StringVar(&globalFlags.configPath, "config", "", "override the global config file")
	pf.StringVar(&globalFlags.keyPath, "key", "", "key file (or ENVER_KEY env)")
	pf.BoolVar(&globalFlags.noLocal, "no-local", false, "ignore the ./.enver.yaml layer when reading")
	pf.BoolVar(&globalFlags.noExpand, "no-expand", false, "do not expand $VAR references")
	pf.BoolVarP(&globalFlags.global, "global", "g", false, "write to the global config instead of the local .enver.yaml (mutating commands)")
	pf.StringVar(&globalFlags.chdir, "chdir", "", "run as if started from this directory (.enver.yaml and relative --config resolve against it)")

	rootCmd.AddCommand(xCmd, showCmd, exportCmd, dotenvCmd, importCmd, listCmd, keygenCmd, encryptCmd, decryptCmd, addCmd, defaultCmd, validateCmd, removeCmd, renameCmd, duplicateCmd, editCmd)

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return applyChdir()
	}

	app.Interactive = ui.Interactive
	app.PromptPassphrase = ui.Password
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
		NoExpand:   globalFlags.noExpand,
	}
}

// applyChdir changes to --chdir before any command logic so that LocalPath and
// relative --config resolve in the target directory. Wired as PersistentPreRunE
// and called by completion functions (which bypass PersistentPreRunE); tests
// call it directly.
func applyChdir() error {
	return app.Chdir(globalFlags.chdir)
}

func requireInteractive(what string) error {
	if ui.Interactive() {
		return nil
	}
	return fmt.Errorf("%s required; pass it as an argument (stdin is not a terminal)", what)
}

func interactiveOnly(name string) error {
	if ui.Interactive() {
		return nil
	}
	return fmt.Errorf("%s is interactive; run it in a terminal", name)
}

// aborted prints the shared notice for a declined destructive confirm and
// returns the write error, so declining exits 0 instead of reading as a
// failure. Call it on the stream the command prints its notices to (stdout).
func aborted(w io.Writer) error {
	_, err := fmt.Fprintln(w, "\naborted")
	return err
}

// writeTarget is the file mutators write: local by default, global under --global.
func writeTarget() string {
	if globalFlags.global {
		return config.GlobalPath(globalFlags.configPath)
	}
	return config.LocalPath()
}

// pickerConfig is the extends-picker set: merged by default (a local profile may
// extend a global one), global-only under --global.
func pickerConfig() (config.Config, error) {
	if globalFlags.global {
		return config.LoadFile(config.GlobalPath(globalFlags.configPath))
	}
	return app.Load(appOpts())
}

// notFoundInTarget hints at --global when the profile lives in the other layer.
func notFoundInTarget(name, target string) error {
	other := config.LocalPath()
	if target == other {
		other = config.GlobalPath(globalFlags.configPath)
	}
	if c, err := config.LoadFile(other); err == nil {
		if _, ok := c.Profiles[name]; ok {
			if globalFlags.global {
				return fmt.Errorf("profile %q not found in global config; it is local — run without --global", name)
			}
			return fmt.Errorf("profile %q not found in local .enver.yaml; it is global — pass --global (-g)", name)
		}
	}
	return fmt.Errorf("profile %q not found", name)
}

func targetProfiles(cmd *cobra.Command, toComplete string) []string {
	if err := applyChdir(); err != nil {
		return nil
	}
	g, _ := cmd.Flags().GetBool("global")
	cfgPath, _ := cmd.Flags().GetString("config")
	target := config.LocalPath()
	if g {
		target = config.GlobalPath(cfgPath)
	}
	cfg, err := config.LoadFile(target)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Profiles))
	for _, n := range cfg.ProfileNames() {
		if strings.HasPrefix(n, toComplete) {
			out = append(out, n)
		}
	}
	return out
}

// completeProfile backs read commands: merged-config completion.
func completeProfile(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if err := applyChdir(); err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	cfgPath, _ := cmd.Flags().GetString("config")
	noLocal, _ := cmd.Flags().GetBool("no-local")
	return app.MatchingProfiles(app.Options{ConfigPath: cfgPath, NoLocal: noLocal}, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeProfileInTarget backs mutators: write-target-scoped completion.
func completeProfileInTarget(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	return targetProfiles(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
}
