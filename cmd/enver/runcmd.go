package main

import (
	"fmt"
	"os"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/runner"
	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:               "run [profile] -- <command> [args...]",
	Short:             "Run a command with a profile's environment injected",
	Args:              cobra.ArbitraryArgs,
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfile,
	RunE:              runRun,
}

func init() {
	runCmd.Flags().BoolVar(&rootFlags.noLocal, "no-local", false, "ignore .enver.yaml layers")
}

func runRun(cmd *cobra.Command, args []string) error {
	return doRun(args, cmd.ArgsLenAtDash())
}

func doRun(args []string, dashAt int) error {
	profile, cmdArgs := parseProfileAndCmd(args, dashAt)
	if len(cmdArgs) == 0 {
		return fmt.Errorf("run requires a command after `--`; to preview the env instead, run `enver <profile>`")
	}
	cfg, err := config.LoadMerged(rootFlags.configPath, !rootFlags.noLocal)
	if err != nil {
		return err
	}
	if profile == "" {
		profile = cfg.Default
	}
	return runProfile(cfg, profile, cmdArgs)
}

func runProfile(cfg config.Config, profile string, cmdArgs []string) error {
	if profile == "" {
		return fmt.Errorf("no profile specified and no `default` set in config")
	}
	env, _, err := resolveAndDecrypt(cfg, profile)
	if err != nil {
		return err
	}
	// Propagate the child's real exit code; cobra's RunE only signals 0/1.
	if code := runner.Run(cmdArgs, runner.MergedEnv(env)); code != 0 {
		os.Exit(code)
	}
	return nil
}

func parseProfileAndCmd(args []string, dashAt int) (profile string, cmdArgs []string) {
	if dashAt >= 0 {
		before := args[:dashAt]
		cmdArgs = args[dashAt:]
		if len(before) > 0 {
			profile = before[0]
		}
		return profile, cmdArgs
	}
	if len(args) > 0 {
		profile = args[0]
	}
	return profile, nil
}
