package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List profiles",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return doList(cmd.OutOrStdout())
	},
}

func doList(w io.Writer) error {
	cfg, err := app.Load(appOpts())
	if err != nil {
		return err
	}
	names := cfg.ProfileNames()
	if len(names) == 0 {
		_, err := fmt.Fprintf(w, "(no profiles defined)\n\nCreate one at: %s\n", config.GlobalPath(globalFlags.configPath))
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
