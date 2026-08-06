package main

import (
	"fmt"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:           "validate",
	Short:         "Check config health (dangling extends, cycles)",
	Args:          cobra.NoArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := app.Load(appOpts())
		if err != nil {
			return err
		}
		w := cmd.OutOrStdout()
		issues := config.Validate(cfg)
		hasErr := false
		for _, is := range issues {
			if is.Severity == "error" {
				hasErr = true
			}
			fmt.Fprintf(w, "%s: %s\n", is.Severity, is)
		}
		if len(issues) == 0 {
			fmt.Fprintln(w, "✓ config is valid")
		}
		if hasErr {
			return fmt.Errorf("config has errors")
		}
		return nil
	},
}
