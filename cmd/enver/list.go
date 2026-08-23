package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/spf13/cobra"
)

var listFormat string

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List profiles",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		switch listFormat {
		case "text":
			return doList(cmd.OutOrStdout())
		case "json":
			return doListJSON(cmd.OutOrStdout())
		default:
			return fmt.Errorf("unsupported list format %q (use text or json)", listFormat)
		}
	},
}

func init() {
	listCmd.Flags().StringVar(&listFormat, "format", "text", "output format: text or json")
	_ = listCmd.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"text", "json"}, cobra.ShellCompDirectiveDefault
	})
}

func doList(w io.Writer) error {
	cfg, err := app.Load(appOpts())
	if err != nil {
		return err
	}
	names := cfg.ProfileNames()
	if len(names) == 0 {
		_, err := fmt.Fprintf(w, "(no profiles defined)\n\nCreate one at: %s\n", writeTarget())
		return err
	}
	type listRow struct {
		marker, name, extends, vars string
	}
	rows := make([]listRow, 0, len(names))
	profWidth, extWidth := len("PROFILE"), len("EXTENDS")
	for _, n := range names {
		p := cfg.Profiles[n]
		marker := " "
		if n == cfg.Default {
			marker = "*"
		}
		extends := strings.Join(p.Extends, ", ")
		if extends == "" {
			extends = "-"
		}
		own := len(p.Env)
		varsCell := fmt.Sprintf("%d", own)
		if len(p.Extends) > 0 {
			if resolved, _, err := cfg.ResolveProfile(n); err == nil {
				varsCell = fmt.Sprintf("%d (→%d)", own, len(resolved))
			}
		}
		rows = append(rows, listRow{marker, n, extends, varsCell})
		profWidth = max(profWidth, len(n))
		extWidth = max(extWidth, len(extends))
	}

	format := fmt.Sprintf("%%-4s %%-%ds %%-%ds %%s\n", profWidth, extWidth)
	if _, err := fmt.Fprintf(w, format, "", "PROFILE", "EXTENDS", "VARS"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(w, format, r.marker, r.name, r.extends, r.vars); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(w, "\n* = default")
	return err
}

// listJSON is the machine-readable shape of `enver list --format json`.
type listJSON struct {
	Profiles []listJSONEntry `json:"profiles"`
}

type listJSONEntry struct {
	Name     string   `json:"name"`
	Default  bool     `json:"default"`
	Extends  []string `json:"extends,omitempty"`
	Vars     int      `json:"vars"`
	Resolved int      `json:"resolved"`
}

func doListJSON(w io.Writer) error {
	cfg, err := app.Load(appOpts())
	if err != nil {
		return err
	}
	names := cfg.ProfileNames()
	out := listJSON{Profiles: make([]listJSONEntry, 0, len(names))}
	for _, n := range names {
		p := cfg.Profiles[n]
		resolved := len(p.Env)
		if len(p.Extends) > 0 {
			r, _, err := cfg.ResolveProfile(n)
			if err != nil {
				return err
			}
			resolved = len(r)
		}
		out.Profiles = append(out.Profiles, listJSONEntry{
			Name:     n,
			Default:  n == cfg.Default,
			Extends:  p.Extends,
			Vars:     len(p.Env),
			Resolved: resolved,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
