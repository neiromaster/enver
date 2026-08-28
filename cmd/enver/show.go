package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
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
		r, err := app.Resolve(cfg, profile, appOpts())
		if err != nil {
			return err
		}
		switch showFormat {
		case "text":
			return printEnv(cmd.OutOrStdout(), r.Env, r.Chain, r.Sources, showNoMask)
		case "json":
			return printEnvJSON(cmd.OutOrStdout(), profile, r.Chain, r.Env, r.Sources)
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
		r, err := app.Resolve(cfg, profile, appOpts())
		if err != nil {
			return err
		}
		switch exportFormat {
		case "bash", "fish", "powershell":
		default:
			return fmt.Errorf("unsupported export format %q (use bash, fish, or powershell)", exportFormat)
		}
		return printExport(cmd.OutOrStdout(), r.Env, exportFormat)
	},
}

var showNoMask bool
var showFormat string
var exportFormat string

func init() {
	showCmd.Flags().BoolVar(&showNoMask, "no-mask", false, "show full secret values")
	showCmd.Flags().StringVar(&showFormat, "format", "text", "output format: text or json")
	exportCmd.Flags().StringVar(&exportFormat, "format", "bash", "output format: bash, fish, or powershell")
	_ = showCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json"}, cobra.ShellCompDirectiveDefault
	})
	_ = exportCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"bash", "fish", "powershell"}, cobra.ShellCompDirectiveDefault
	})
}

// printEnv writes the resolved env to w as `K=V`, masked unless unmasked. When
// provenance is present, each line is annotated with the defining profile and
// layer (`# from anth (global)`) so the chain header stays a summary and the
// per-key winner is debuggable.
func printEnv(w io.Writer, env map[string]string, chain []string, sources map[string]config.Source, unmasked bool) error {
	if _, err := fmt.Fprintf(w, "# profile: %s\n", strings.Join(chain, " → ")); err != nil {
		return err
	}
	for _, k := range slices.Sorted(maps.Keys(env)) {
		v := env[k]
		if !unmasked {
			v = config.MaskValue(k, v)
		}
		if s, ok := sources[k]; ok {
			if _, err := fmt.Fprintf(w, "%s=%s  # from %s (%s)\n", k, v, s.Profile, s.Layer); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(w, "%s=%s\n", k, v); err != nil {
			return err
		}
	}
	return nil
}

// showJSON is the machine-readable shape of `enver show --format json`.
type showJSON struct {
	Profile string                   `json:"profile"`
	Chain   []string                 `json:"chain"`
	Env     map[string]string        `json:"env"`
	Sources map[string]config.Source `json:"sources,omitempty"`
}

// printEnvJSON writes the resolved env as JSON, always unmasked: JSON is a
// machine contract (export, piping to tools), so masking would corrupt the
// values a consumer reads back. Provenance rides along per key.
func printEnvJSON(w io.Writer, profile string, chain []string, env map[string]string, sources map[string]config.Source) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(showJSON{Profile: profile, Chain: chain, Env: env, Sources: sources})
}

// printExport writes the resolved env as shell assignments for eval. Unset
// keys are simply absent from env, so `eval "$(enver export p)"` leaves any
// shell-exported value untouched — matching what `enver x p` gives the child.
// bash emits `export K='V'`; fish emits `set -gx K 'V'`; powershell emits
// `$env:K = 'V'`. Values are always unmasked.
func printExport(w io.Writer, env map[string]string, format string) error {
	for _, k := range slices.Sorted(maps.Keys(env)) {
		v := env[k]
		var line string
		switch format {
		case "fish":
			line = fmt.Sprintf("set -gx %s %s", k, fishQuote(v))
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

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// psQuote escapes a value for a PowerShell single-quoted string: a literal
// quote is doubled.
func psQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// fishQuote escapes a value for a fish single-quoted string. fish treats only
// \\ and \' as escapes inside single quotes, so both must be doubled to make
// any value round-trip (notably Windows backslash paths).
func fishQuote(s string) string {
	var b strings.Builder
	b.WriteByte('\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' || s[i] == '\'' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('\'')
	return b.String()
}
