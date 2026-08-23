package main

import (
	"encoding/json"
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
		switch showFormat {
		case "text":
			return printEnv(cmd.OutOrStdout(), env, chain, showNoMask)
		case "json":
			return printEnvJSON(cmd.OutOrStdout(), profile, chain, env)
		default:
			return fmt.Errorf("unsupported show format %q (use text or json)", showFormat)
		}
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
		env, _, err := app.Resolve(cfg, profile, appOpts())
		if err != nil {
			return err
		}
		switch exportFormat {
		case "bash", "powershell":
		default:
			return fmt.Errorf("unsupported export format %q (use bash or powershell)", exportFormat)
		}
		return printExport(cmd.OutOrStdout(), env, exportFormat)
	},
}

var showNoMask bool
var showFormat string
var exportFormat string

func init() {
	showCmd.Flags().BoolVar(&showNoMask, "no-mask", false, "show full secret values")
	showCmd.Flags().StringVar(&showFormat, "format", "text", "output format: text or json")
	exportCmd.Flags().StringVar(&exportFormat, "format", "bash", "output format: bash or powershell")
	_ = showCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json"}, cobra.ShellCompDirectiveDefault
	})
	_ = exportCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"bash", "powershell"}, cobra.ShellCompDirectiveDefault
	})
}

// printEnv writes the resolved env to w as `K=V`, masked unless unmasked.
func printEnv(w io.Writer, env map[string]string, chain []string, unmasked bool) error {
	if _, err := fmt.Fprintf(w, "# profile: %s\n", strings.Join(chain, " → ")); err != nil {
		return err
	}
	for _, k := range sortedEnvKeys(env) {
		v := env[k]
		if !unmasked {
			v = config.MaskValue(k, v)
		}
		if _, err := fmt.Fprintf(w, "%s=%s\n", k, v); err != nil {
			return err
		}
	}
	return nil
}

// showJSON is the machine-readable shape of `enver show --format json`.
type showJSON struct {
	Profile string            `json:"profile"`
	Chain   []string          `json:"chain"`
	Env     map[string]string `json:"env"`
}

// printEnvJSON writes the resolved env as JSON, always unmasked: JSON is a
// machine contract (export, piping to tools), so masking would corrupt the
// values a consumer reads back.
func printEnvJSON(w io.Writer, profile string, chain []string, env map[string]string) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(showJSON{Profile: profile, Chain: chain, Env: env})
}

// printExport writes the resolved env as shell assignments for eval. bash emits
// `export K='V'`; powershell emits `$env:K = 'V'`. Values are always unmasked.
func printExport(w io.Writer, env map[string]string, format string) error {
	for _, k := range sortedEnvKeys(env) {
		v := env[k]
		var line string
		switch format {
		case "powershell":
			line = fmt.Sprintf("$env:%s = '%s'", k, psQuote(v))
		default:
			line = fmt.Sprintf("export %s=%s", k, shellQuote(v))
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// psQuote escapes a value for a PowerShell single-quoted string: a literal
// quote is doubled.
func psQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
