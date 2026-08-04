package main

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/runner"
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = ""
	date    = ""
)

func formatVersion(version, commit, date string) string {
	meta := make([]string, 0, 2)
	if commit != "" {
		meta = append(meta, commit)
	}
	if date != "" {
		meta = append(meta, date)
	}
	if len(meta) == 0 {
		return version
	}
	return version + " (" + strings.Join(meta, ", ") + ")"
}

func buildSetting(bi *debug.BuildInfo, key string) string {
	for _, s := range bi.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

func resolveFromBuildInfo(bi *debug.BuildInfo) (string, string, string) {
	if bi == nil {
		return "", "", ""
	}
	v := ""
	if mv := bi.Main.Version; mv != "" && mv != "(devel)" {
		v = mv
	}
	return v, buildSetting(bi, "vcs.revision"), buildSetting(bi, "vcs.time")
}

func buildVersion() string {
	v, c, d := version, commit, date
	if v == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			if bv, bc, bd := resolveFromBuildInfo(bi); bv != "" {
				v = bv
				c, d = bc, bd
			}
		}
	}
	return formatVersion(v, c, d)
}

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
	Use:   "enver [profile] -- <command> [args...]",
	Short: "Inject environment variables from layered YAML profiles into a command",
	Long: `enver — environment profile injector.

Inject environment variables from a layered YAML config into a child command,
without mutating any tool's own config.

Config locations (merged in order, later wins):
  1. $XDG_CONFIG_HOME/enver/config.yaml  (or ~/.config/enver/config.yaml)
  2. .enver.yaml walked from cwd up to (not including) $HOME

When no profile is given, the config's ` + "`default`" + ` is used.

The subcommand names init, keygen, encrypt, decrypt and completion are reserved
and cannot be used as profile names.

Examples:
  enver anth -- claude                       # run claude with the "anth" profile
  enver -- claude                            # run claude with the default profile
  enver anth                                 # preview resolved env (masked)
  eval "$(enver anth --export)"              # apply to current shell`,
	Args:              cobra.ArbitraryArgs,
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfile,
	RunE:              runRoot,
}

func init() {
	rootCmd.Version = buildVersion()

	pf := rootCmd.PersistentFlags()
	pf.StringVar(&rootFlags.configPath, "config", "", "override the global config file")
	pf.StringVar(&rootFlags.keyPath, "key", "", "key file (or ENVER_KEY env)")

	lf := rootCmd.Flags()
	lf.BoolVarP(&rootFlags.listMode, "list", "l", false, "list profiles")
	lf.BoolVar(&rootFlags.printMode, "print", false, "print resolved env (masked)")
	lf.BoolVar(&rootFlags.exportMode, "export", false, "print `export K=V` for eval")
	lf.BoolVar(&rootFlags.noLocal, "no-local", false, "ignore .enver.yaml layers")
	lf.BoolVar(&rootFlags.noMask, "no-mask", false, "show full secrets with --print")

	rootCmd.AddCommand(keygenCmd, encryptCmd, decryptCmd, initCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		os.Exit(1)
	}
}

func runRoot(cmd *cobra.Command, args []string) error {
	// Args after `--` are the child command (cobra leaves them unparsed).
	dashAt := cmd.ArgsLenAtDash()
	var profile string
	var cmdArgs []string
	if dashAt >= 0 {
		before := args[:dashAt]
		cmdArgs = args[dashAt:]
		if len(before) > 0 {
			profile = before[0]
		}
	} else if len(args) > 0 {
		profile = args[0]
	}

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

	if profile == "" {
		return fmt.Errorf("no profile specified and no `default` set in config")
	}
	env, _, err := resolveAndDecrypt(cfg, profile)
	if err != nil {
		return err
	}
	// The child's exit code must be propagated exactly; cobra only signals 0/1.
	if code := runner.Run(cmdArgs, runner.MergedEnv(env)); code != 0 {
		os.Exit(code)
	}
	return nil
}

// completeProfile powers dynamic shell completion of profile names.
func completeProfile(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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
