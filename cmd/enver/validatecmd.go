package main

import (
	"cmp"
	"fmt"
	"slices"

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
		// Isolated-global catches a global profile extending a local-only name.
		globalIssues, err := config.ValidateGlobal(config.GlobalPath(globalFlags.configPath))
		if err != nil {
			return err
		}
		issues := dedupIssues(append(
			config.Validate(cfg),
			globalIssues...,
		))
		hasErr := false
		for _, is := range issues {
			if is.Severity == "error" {
				hasErr = true
			}
			if is.File != "" {
				if _, err := fmt.Fprintf(w, "%s: %s: %s\n", is.File, is.Severity, is); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintf(w, "%s: %s\n", is.Severity, is); err != nil {
					return err
				}
			}
		}
		if len(issues) == 0 {
			if _, err := fmt.Fprintln(w, "✓ config is valid"); err != nil {
				return err
			}
		}
		if hasErr {
			return fmt.Errorf("config has errors")
		}
		return nil
	},
}

// dedupIssues collapses findings both passes report, preferring the file-scoped
// variant; sorted by file then profile.
func dedupIssues(issues []config.Issue) []config.Issue {
	type key struct {
		profile string
		kind    config.IssueKind
		target  string
	}
	best := map[key]config.Issue{}
	for _, is := range issues {
		k := key{is.Profile, is.Kind, is.Target}
		cur, ok := best[k]
		if !ok || (is.File != "" && cur.File == "") {
			best[k] = is
		}
	}
	out := make([]config.Issue, 0, len(best))
	for _, is := range best {
		out = append(out, is)
	}
	slices.SortFunc(out, func(a, b config.Issue) int {
		return cmp.Or(
			cmp.Compare(a.File, b.File),
			cmp.Compare(a.Profile, b.Profile),
			cmp.Compare(a.Kind, b.Kind),
		)
	})
	return out
}
