package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/dotenv"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

var (
	dotenvOut      string
	dotenvNoHeader bool
	dotenvForce    bool
)

// confirmFunc mirrors ui.Confirm so the overwrite prompt can be stubbed in tests.
type confirmFunc func(string, bool) (bool, error)

var dotenvCmd = &cobra.Command{
	Use:               "dotenv [profile]",
	Short:             "Export a profile to a .env file (with comments)",
	Args:              cobra.MaximumNArgs(1),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfile,
	RunE: func(cmd *cobra.Command, args []string) error {
		profile := ""
		if len(args) > 0 {
			profile = args[0]
		}
		return runDotenv(cmd.OutOrStdout(), profile, dotenvOut, dotenvNoHeader, dotenvForce, ui.Confirm)
	},
}

func init() {
	dotenvCmd.Flags().StringVarP(&dotenvOut, "out", "o", "", "write to <file> instead of stdout")
	dotenvCmd.Flags().BoolVar(&dotenvNoHeader, "no-header", false, "omit the generated-by header")
	dotenvCmd.Flags().BoolVar(&dotenvForce, "force", false, "overwrite --out target without prompting")
}

// runDotenv resolves the profile, formats it as a .env document, and writes the
// result to stdout (outPath == "") or to outPath. confirm is consulted only when
// outPath exists and force is false. A comment-resolution error is non-fatal:
// the file is still produced, with fewer or no comments.
func runDotenv(stdout io.Writer, profile, outPath string, noHeader, force bool, confirm confirmFunc) error {
	resolveOpts := appOpts()
	resolveOpts.NoExpand = true // dotenv emits raw templates, not expanded values
	profile, r, err := app.ResolveDefault(resolveOpts, profile)
	if err != nil {
		return err
	}

	attribution := make(map[string]string, len(r.Unsets))
	for k, s := range r.Unsets {
		attribution[k] = s.Profile
	}
	out := dotenv.Format(r.Env, r.Comments, attribution, dotenv.Options{Header: !noHeader, Profile: profile, Chain: r.Chain})

	if outPath == "" {
		_, err = stdout.Write(out)
		return err
	}
	return writeDotenvFile(stdout, outPath, out, force, confirm, len(r.Env))
}

// writeDotenvFile writes content to path with mode 0600 (it holds decrypted
// secrets). If path already exists and force is false, the caller is prompted via
// confirm; a declined prompt prints "aborted" and returns nil, while a failed
// prompt (e.g. non-interactive shell) errors with a --force hint. On success, a
// one-line plaintext confirmation is written to stdout.
func writeDotenvFile(stdout io.Writer, path string, content []byte, force bool, confirm confirmFunc, n int) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			ok, cerr := confirm(fmt.Sprintf("Overwrite %s?", path), false)
			if cerr != nil {
				return fmt.Errorf("output file %q exists; pass --force to overwrite", path)
			}
			if !ok {
				return aborted(stdout)
			}
		}
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "✓ wrote %s (%d vars) — secrets stored in plaintext.\n", filepath.Base(path), n); err != nil {
		return err
	}
	return nil
}
