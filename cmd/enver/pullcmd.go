package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/remote"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

// pullFetch is the network seam: production fetches with remote.Fetch, tests
// replace it so a pull runs hermetic.
var pullFetch = remote.Fetch

// pullGistHead is the gist-revision seam, replaced by tests like pullFetch.
var pullGistHead = remote.GistHead

var (
	pullAs       string
	pullLocal    bool
	pullNoPrefix bool
	pullRm       bool
	pullForce    bool
	pullList     bool
)

var pullCmd = &cobra.Command{
	Use:   "pull [<source>|<name>]",
	Short: "Fetch a remote config and register it",
	Long: `Fetch a remote config, validate it, and store it where every read command
loads it as the lowest layer.

  enver pull <source> --as <name>       register a source and fetch it now
  enver pull <name>                     re-fetch one registered remote
  enver pull                            re-fetch every registered remote
  enver pull --list                     list registered remotes and their last pull
  enver pull --rm <name>                remove the record (and its content)

A source is github:owner/repo[@ref], optionally with a path inside the repo,
gist:owner/id[/file][@revision] (a gist has no branches or tags, so a revision
is the full 40-character sha), or an https URL. Registration writes the global
config; --local writes the project's .enver.yaml instead, so the record travels
with the repository. A remote's profiles appear under its record name as a
prefix, unless --no-prefix stores them under their bare upstream names.

Nothing is resolved from the network when a profile is read: run enver pull
again to refresh. Removing a record deletes its content only when no visible
layer still declares it, and -g/--local target an explicit layer instead of the
closest declaration.`,
	Args:              cobra.MaximumNArgs(1),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completePull,
	RunE: func(cmd *cobra.Command, args []string) error {
		if pullLocal && globalFlags.file != "" {
			return fmt.Errorf("--local and --file conflict; --file is the whole config")
		}
		if pullLocal && globalFlags.global {
			return fmt.Errorf("--local and --global conflict; pick the layer the record belongs to")
		}
		if pullList {
			return runPullList(cmd, args)
		}
		if pullRm {
			if len(args) == 0 {
				return fmt.Errorf("--rm requires a remote name")
			}
			if pullAs != "" {
				return fmt.Errorf("--as names a record being registered; --rm removes a record that already exists")
			}
			if pullNoPrefix {
				return fmt.Errorf("--no-prefix sets how a registration stores its profiles; --rm removes a record instead")
			}
			if pullForce {
				return fmt.Errorf("--force accepts a takeover without confirming; --rm removes the record without a prompt")
			}
			opts, err := pullOptions()
			if err != nil {
				return err
			}
			opts.LayerPinned = pullLocal || globalFlags.global
			opts.OtherHidden = globalFlags.noLocal
			summary, err := app.PullRemove(args[0], opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), summary)
			return err
		}
		opts, err := pullOptions()
		if err != nil {
			return err
		}
		summary, err := dispatchPull(opts, args)
		if err != nil {
			return err
		}
		if summary == "" {
			return aborted(cmd.OutOrStdout())
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), summary)
		return err
	},
}

// runPull dispatches on the argument: nothing pulls every registered remote, a
// record name re-fetches that one, and anything else is a source to register
// under --as.
func dispatchPull(opts app.PullOptions, args []string) (string, error) {
	if len(args) == 0 {
		if pullAs != "" {
			return "", fmt.Errorf("--as names the record for a source; run enver pull <source> --as <name>")
		}
		return app.PullAll(opts)
	}
	raw := args[0]
	if !config.ValidRecordName(raw) {
		if _, _, err := remote.SplitRef(raw); err != nil {
			return "", fmt.Errorf("%q is neither a source enver can fetch nor a registered remote name: %w", raw, err)
		}
		if pullAs == "" {
			return "", fmt.Errorf("registering a source needs --as <name>, the record name its profiles are prefixed with")
		}
		return app.Pull(raw, pullAs, opts)
	}
	if pullAs != "" {
		return "", fmt.Errorf("--as applies only when registering a source; %q is a recorded remote name", raw)
	}
	return app.PullUpdate(raw, opts)
}

// pullOptions assembles the run: the merged view the gate judges against, the
// two user layers with the write target first, and the injected seams. Under
// --file the file is the whole config, so it is the target and there is no
// other layer.
func pullOptions() (app.PullOptions, error) {
	merged, err := app.Load(appOpts())
	if err != nil {
		return app.PullOptions{}, err
	}
	target, other := config.GlobalPath(globalFlags.configPath), config.LocalPath()
	if pullLocal {
		target, other = other, target
	}
	if globalFlags.file != "" {
		target, other = globalFlags.file, ""
	}
	opts := app.PullOptions{
		TargetIsGlobal: !pullLocal && globalFlags.file == "",
		NoPrefix:       pullNoPrefix,
		Force:          pullForce,
		Merged:         merged,
		Fetch:          pullFetch,
		GistHead:       pullGistHead,
		Confirm:        ui.Confirm,
		TargetPath:     target,
		OtherPath:      other,
	}
	if opts.Target, err = config.LoadFile(opts.TargetPath); err != nil {
		return app.PullOptions{}, err
	}
	// Under --no-local the local layer is not part of the view a registration or
	// a re-fetch judges, so it is not read and cannot block one. A removal is
	// different: deleting content a local record still declares would blank every
	// profile under its prefix, so --rm always sees both layers. Under --file
	// OtherPath is empty: there is no other layer to read.
	if opts.OtherPath != "" && (!globalFlags.noLocal || pullRm) {
		if opts.Other, err = config.LoadFile(opts.OtherPath); err != nil {
			return app.PullOptions{}, err
		}
	}
	return opts, nil
}

// completePull offers the record names the active view registers: the merged
// config by default, the standalone file under --file. It loads the config
// directly rather than through app.Load so no load-time warning can leak
// into a shell's completion output.
func completePull(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	if err := applyGlobalFlags(); err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	var cfg config.Config
	var err error
	if file, _ := cmd.Flags().GetString("file"); file != "" {
		cfg, err = config.LoadStandalone(file)
	} else {
		cfgPath, _ := cmd.Flags().GetString("config")
		noLocal, _ := cmd.Flags().GetBool("no-local")
		cfg, err = config.LoadMerged(cfgPath, !noLocal)
	}
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	var out []string
	for _, name := range slices.Sorted(maps.Keys(cfg.Remotes)) {
		if strings.HasPrefix(name, toComplete) {
			out = append(out, name)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func init() {
	pullCmd.Flags().StringVar(&pullAs, "as", "", "record name for a source being registered")
	pullCmd.Flags().BoolVar(&pullLocal, "local", false, "write the record to ./.enver.yaml instead of the global config")
	pullCmd.Flags().BoolVar(&pullNoPrefix, "no-prefix", false, "store the remote's profiles under their bare upstream names")
	pullCmd.Flags().BoolVar(&pullRm, "rm", false, "remove the record (and its content when no layer still declares it)")
	pullCmd.Flags().BoolVar(&pullForce, "force", false, "accept a takeover of existing profiles without confirming")
	pullCmd.Flags().BoolVar(&pullList, "list", false, "list registered remotes and their last-pulled commit and time")
}
