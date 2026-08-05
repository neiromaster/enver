package main

import (
	"fmt"
	"io"

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
	if _, err := fmt.Fprintf(w, "%-4s %-20s %-16s %s\n", "", "PROFILE", "EXTENDS", "VARS"); err != nil {
		return err
	}
	for _, n := range names {
		p := cfg.Profiles[n]
		marker := " "
		if n == cfg.Default {
			marker = "*"
		}
		extends := p.Extends
		if extends == "" {
			extends = "-"
		}
		if _, err := fmt.Fprintf(w, "%-4s %-20s %-16s %d\n", marker, n, extends, len(p.Env)); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(w, "\n* = default")
	return err
}
