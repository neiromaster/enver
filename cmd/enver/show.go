package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/envname"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:               "show [profiles...]",
	Short:             "Preview a profile's resolved environment (masked)",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: completeProfile,
	RunE: func(cmd *cobra.Command, args []string) error {
		switch showFormat {
		case "text", "json":
		default:
			return fmt.Errorf("unsupported show format %q (use text or json)", showFormat)
		}
		opts := appOpts()
		// Text can say what it cannot read; JSON hands values to machines, where
		// ciphertext is a trap and a non-zero exit is the honest answer. Remote
		// ciphertext is the one value a key holder may legitimately not read
		// either way: both formats report it per key, and JSON keeps it out of
		// env entirely.
		opts.TolerateMissingKey = showFormat == "text"
		opts.TolerateRemoteWrongKey = true
		p, r, err := app.ResolveProfiles(opts, args)
		if err != nil {
			return err
		}
		if showFormat == "json" {
			return printEnvJSON(cmd.OutOrStdout(), p, r.Chain, r.Env, r.Sources, r.Unsets, r.Undecryptable)
		}
		out := cmd.OutOrStdout()
		return printEnv(out, r.Env, r.Chain, r.Sources, r.Unsets, envRender{
			unmasked:      showNoMask,
			color:         colorEnabled(out),
			marked:        r.Marked,
			encrypted:     r.Encrypted,
			undecryptable: r.Undecryptable,
		})
	},
}

var exportCmd = &cobra.Command{
	Use:               "export [profiles...]",
	Short:             "Print `export K=V` for a profile (unmasked, for eval)",
	Args:              cobra.ArbitraryArgs,
	ValidArgsFunction: completeProfile,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, r, err := app.ResolveProfiles(appOpts(), args)
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
		return []string{"text", "json"}, cobra.ShellCompDirectiveNoFileComp
	})
	_ = exportCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"bash", "fish", "powershell"}, cobra.ShellCompDirectiveNoFileComp
	})
}

// envRender carries printEnv's text toggles so call sites read by name
// instead of trailing positional bools.
type envRender struct {
	unmasked bool
	color    bool
	// marked and encrypted are the resolved secret marks and at-rest
	// encryption state, both nil when empty.
	marked    map[string]bool
	encrypted map[string]bool
	// undecryptable maps a key to why its ciphertext stayed unread; nil unless
	// no key source resolved.
	undecryptable map[string]string
}

// layerLabel is the layer name a provenance render shows: under --file the one
// file is the whole config, so a global-labeled entry presents as the file
// layer.
func layerLabel(layer string) string {
	if globalFlags.file != "" && layer == config.LayerGlobal {
		return config.LayerFile
	}
	return layer
}

// relabelSources restamps each source's layer through layerLabel, so the JSON
// provenance names layers the way the text comment does.
func relabelSources(sources map[string]config.Source) map[string]config.Source {
	if globalFlags.file == "" {
		return sources
	}
	out := make(map[string]config.Source, len(sources))
	for k, s := range sources {
		s.Layer = layerLabel(s.Layer)
		out[k] = s
	}
	return out
}

// markNote flags a row whose value carries the secret mark but still sits in
// plaintext: the mark's promise of encryption is not kept yet.
const markNote = "~ marked, not encrypted"

// printEnv writes the resolved env to w as `K=V`, masked unless
// opts.unmasked is set: a marked or at-rest-encrypted value masks whatever its
// name looks like, and a marked row still in plaintext says so. A key whose
// ciphertext could not be read carries the reason in place of its value —
// masking or revealing it would both pretend to know what it holds. When
// provenance is present, each line is annotated with the defining profile and
// layer (`# from anth (global)`) so the chain header stays a summary and the
// per-key winner is debuggable. Unset keys interleave in the same sort as
// comment rows (`# KEY — unset by "anth"`), mirroring the dotenv export: the
// deliberately absent variables stay visible next to the live ones. A live key
// wins its row — Resolved keeps env and unsets disjoint, and the sorted union
// collapses any violation rather than rendering it. With opts.color set, keys
// render bold and every comment (header, provenance suffix, tombstones) renders
// dim; the caller gates styling on the destination being a terminal (never
// under NO_COLOR or TERM=dumb) so pipes stay clean.
func printEnv(w io.Writer, env map[string]string, chain []string, sources, unsets map[string]config.Source, opts envRender) error {
	var dim, bold lipgloss.Style
	if opts.color {
		dim, bold = dimStyle, boldStyle
	}
	if _, err := fmt.Fprintln(w, dim.Render("# profile: "+strings.Join(chain, " → "))); err != nil {
		return err
	}
	for _, k := range envname.SortedUnion(env, unsets) {
		v, live := env[k]
		if !live {
			if _, err := fmt.Fprintln(w, dim.Render(fmt.Sprintf("# %s — unset by %q", k, unsets[k].Profile))); err != nil {
				return err
			}
			continue
		}
		marked, encrypted := opts.marked[k], opts.encrypted[k]
		if reason, ok := opts.undecryptable[k]; ok {
			v = "(cannot decrypt: " + reason + ")"
		} else if !opts.unmasked {
			v = config.MaskValue(v, config.ShouldMask(v, marked, encrypted))
		}
		comment := ""
		if s, ok := sources[k]; ok {
			comment = fmt.Sprintf("# from %s (%s)", s.Profile, layerLabel(s.Layer))
		}
		if marked && !encrypted {
			comment = strings.TrimSpace(comment + " " + markNote)
		}
		line := bold.Render(k) + "=" + v
		if comment != "" {
			line += "  " + dim.Render(comment)
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
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
	Unsets  map[string]config.Source `json:"unsets,omitempty"`
	// Undecryptable names the keys whose ciphertext stayed unread, with the
	// reason. Those keys are absent from Env, so no consumer acts on ciphertext;
	// the map is omitted when every value was readable.
	Undecryptable map[string]string `json:"undecryptable,omitempty"`
}

// printEnvJSON writes the resolved env as JSON, always unmasked: JSON is a
// machine contract (export, piping to tools), so masking would corrupt the
// values a consumer reads back. Provenance rides along per key, as do the
// unset tombstones — omitted entirely when there are none, so profiles
// without unsets keep their previous shape. A key whose ciphertext stayed unread
// is reported instead of rendered: handing out enc:v3: text where a value
// belongs is the trap the report exists to avoid.
func printEnvJSON(w io.Writer, profile string, chain []string, env map[string]string, sources, unsets map[string]config.Source, undecryptable map[string]string) error {
	sources = relabelSources(sources)
	unsets = relabelSources(unsets)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(showJSON{Profile: profile, Chain: chain, Env: readableOnly(env, undecryptable), Sources: sources, Unsets: unsets, Undecryptable: undecryptable})
}

// readableOnly drops the keys reported unreadable from a machine payload: the
// report says why they are absent, and nothing downstream mistakes ciphertext
// for a value.
func readableOnly(env map[string]string, undecryptable map[string]string) map[string]string {
	if len(undecryptable) == 0 {
		return env
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if _, unread := undecryptable[k]; !unread {
			out[k] = v
		}
	}
	return out
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
