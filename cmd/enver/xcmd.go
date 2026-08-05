package main

import (
	"github.com/neiromaster/enver/internal/app"
	"github.com/spf13/cobra"
)

var xCmd = &cobra.Command{
	Use:               "x [profile] -- <command> [args...]",
	Short:             "Run a command with a profile's environment injected",
	Args:              cobra.ArbitraryArgs,
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfile,
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := appOpts()
		opts.Name = "enver x"
		return app.Run(args, cmd.ArgsLenAtDash(), opts)
	},
}
