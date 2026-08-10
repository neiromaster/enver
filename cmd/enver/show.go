package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:               "show [profile]",
	Short:             "Preview a profile's resolved environment (masked)",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeProfile,
	RunE: func(cmd *cobra.Command, args []string) error {
		profile := ""
		if len(args) > 0 {
			profile = args[0]
		}
		cfg, err := app.Load(appOpts())
		if err != nil {
			return err
		}
		profile, err = app.ProfileOrDefault(profile, cfg.Default)
		if err != nil {
			return err
		}
		env, chain, err := app.Resolve(cfg, profile, appOpts())
		if err != nil {
			return err
		}
		return printEnv(cmd.OutOrStdout(), env, chain, false, showNoMask)
	},
}

var exportCmd = &cobra.Command{
	Use:               "export [profile]",
	Short:             "Print `export K=V` for a profile (unmasked, for eval)",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeProfile,
	RunE: func(cmd *cobra.Command, args []string) error {
		profile := ""
		if len(args) > 0 {
			profile = args[0]
		}
		cfg, err := app.Load(appOpts())
		if err != nil {
			return err
		}
		profile, err = app.ProfileOrDefault(profile, cfg.Default)
		if err != nil {
			return err
		}
		env, chain, err := app.Resolve(cfg, profile, appOpts())
		if err != nil {
			return err
		}
		return printEnv(cmd.OutOrStdout(), env, chain, true, true)
	},
}

var showNoMask bool

func init() {
	showCmd.Flags().BoolVar(&showNoMask, "no-mask", false, "show full secret values")
}

// printEnv writes the resolved env to w. When exportFmt is true it emits
// `export K=V` (always unmasked); otherwise `K=V`, masked unless unmasked.
func printEnv(w io.Writer, env map[string]string, chain []string, exportFmt, unmasked bool) error {
	if !exportFmt {
		if _, err := fmt.Fprintf(w, "# profile: %s\n", strings.Join(chain, " → ")); err != nil {
			return err
		}
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := env[k]
		if exportFmt {
			if _, err := fmt.Fprintf(w, "export %s=%s\n", k, shellQuote(v)); err != nil {
				return err
			}
			continue
		}
		if !unmasked {
			v = config.MaskValue(k, v)
		}
		if _, err := fmt.Fprintf(w, "%s=%s\n", k, v); err != nil {
			return err
		}
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
