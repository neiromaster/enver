package main

import (
	"fmt"
	"os"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/version"
	"github.com/spf13/cobra"
)

var rootFlags struct {
	configPath string
	keyPath    string
	noLocal    bool
	noMask     bool
	printMode  bool
	exportMode bool
	listMode   bool
}

var rootCmd = &cobra.Command{
	Use:   "enver [run] [profile] -- <command> [args...]",
	Short: "Inject environment variables from layered YAML profiles into a command",
	Long: `enver — environment profile injector.

Inject environment variables from a layered YAML config into a child command,
without mutating any tool's own config.

Run a profile with ` + "`enver run <profile> -- <command>`" + `; the shorthand
` + "`enver <profile> -- <command>`" + ` also works. When no profile is given, the
config's ` + "`default`" + ` is used.

The first positional token is matched against subcommand names (run, init,
keygen, encrypt, decrypt, completion) before being treated as a profile. To run
a profile whose name collides with a subcommand, use the ` + "`run`" + ` verb.

Config locations (merged in order, later wins):
  1. $XDG_CONFIG_HOME/enver/config.yaml  (or ~/.config/enver/config.yaml)
  2. .enver.yaml walked from cwd up to (not including) $HOME

Examples:
  enver run anth -- claude                   # run claude with the "anth" profile
  enver run -- claude                        # run claude with the default profile
  enver anth -- claude                       # shorthand for running
  enver anth                                 # preview resolved env (masked)
  eval "$(enver anth --export)"              # apply env to the current shell`,
	Args:              cobra.ArbitraryArgs,
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfile,
	RunE:              runRoot,
}

func init() {
	rootCmd.Version = version.String()

	pf := rootCmd.PersistentFlags()
	pf.StringVar(&rootFlags.configPath, "config", "", "override the global config file")
	pf.StringVar(&rootFlags.keyPath, "key", "", "key file (or ENVER_KEY env)")

	lf := rootCmd.Flags()
	lf.BoolVarP(&rootFlags.listMode, "list", "l", false, "list profiles")
	lf.BoolVar(&rootFlags.printMode, "print", false, "print resolved env (masked)")
	lf.BoolVar(&rootFlags.exportMode, "export", false, "print `export K=V` for eval")
	lf.BoolVar(&rootFlags.noLocal, "no-local", false, "ignore .enver.yaml layers")
	lf.BoolVar(&rootFlags.noMask, "no-mask", false, "show full secrets with --print")

	rootCmd.AddCommand(keygenCmd, encryptCmd, decryptCmd, initCmd, runCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		os.Exit(1)
	}
}

func runRoot(cmd *cobra.Command, args []string) error {
	profile, cmdArgs := parseProfileAndCmd(args, cmd.ArgsLenAtDash())

	if rootFlags.listMode {
		return doList()
	}

	cfg, err := config.LoadMerged(rootFlags.configPath, !rootFlags.noLocal)
	if err != nil {
		return err
	}

	if profile == "" {
		profile = cfg.Default
	}

	switch {
	case rootFlags.exportMode:
		if profile == "" {
			return fmt.Errorf("no profile specified and no `default` set in config")
		}
		return doPrint(cfg, profile, true, true)
	case rootFlags.printMode:
		if profile == "" {
			return fmt.Errorf("no profile specified and no `default` set in config")
		}
		return doPrint(cfg, profile, false, rootFlags.noMask)
	case len(cmdArgs) == 0:
		// bare `enver` lists; `enver <profile>` previews
		if profile == "" {
			return doList()
		}
		return doPrint(cfg, profile, false, rootFlags.noMask)
	}

	return runProfile(cfg, profile, cmdArgs)
}

// completeProfile powers dynamic shell completion of profile names.
func completeProfile(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	// Past the profile, defer to the shell so the child command completes from PATH.
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	cfg, err := config.LoadMerged(rootFlags.configPath, !rootFlags.noLocal)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := cfg.ProfileNames()
	out := make([]string, 0, len(names))
	for _, n := range names {
		if hasPrefix(n, toComplete) {
			out = append(out, n)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
