package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/dotenv"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

// completeImport drives shell completion for `enver import`: the first positional
// argument is a .env file path (default file completion), the second is a profile
// name (profile completion). This inverts completeProfile, which assumes arg 0 is
// a profile and is wrong for import.
func completeImport(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	return targetProfiles(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
}

var (
	importReplace bool
	importForce   bool
	importExtends string
)

var importCmd = &cobra.Command{
	Use:               "import <file> [profile]",
	Short:             "Import a .env file into a profile",
	Args:              cobra.RangeArgs(1, 2),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeImport,
	RunE: func(cmd *cobra.Command, args []string) error {
		file := args[0]
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		var r io.Reader
		if file == "-" {
			r = os.Stdin
		} else {
			f, err := os.Open(file)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			r = f
		}
		if name == "" {
			if err := requireInteractive("profile name"); err != nil {
				return err
			}
			n, ok := promptProfileName()
			if !ok {
				return nil
			}
			name = n
		} else if err := validateProfileName(name); err != nil {
			return err
		}
		summary, err := runImport(r, config.GlobalPath(globalFlags.configPath), name, importReplace, importForce, importExtends, ui.Confirm)
		if err != nil {
			return err
		}
		if summary != "" {
			fmt.Print(summary)
		} else {
			fmt.Println("aborted")
		}
		return nil
	},
}

func init() {
	importCmd.Flags().BoolVar(&importReplace, "replace", false, "wipe the profile's own env before importing")
	importCmd.Flags().BoolVar(&importForce, "force", false, "skip the --replace removal confirmation")
	importCmd.Flags().StringVar(&importExtends, "extends", "", "set or override the profile's extends (comma-separated for multiple)")
}

// runImport parses .env data from r into profile name at cfgPath. Imported keys
// override existing same-named keys (merge); when replace is true the profile's
// own env is wiped first. The extends value is preserved unless extendsFlag is
// non-empty, in which case it is set (and the parent must already exist). force
// and confirm gate destructive replaces (Task 6). Returns a one-line summary.
func runImport(r io.Reader, cfgPath, name string, replace, force bool, extendsFlag string, confirm confirmFunc) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	entries, err := dotenv.Parse(data)
	if err != nil {
		return "", err
	}
	imported := make(map[string]string, len(entries))
	comments := map[string]string{}
	for _, e := range entries {
		imported[e.Key] = e.Value
		if e.Comment != "" {
			comments[e.Key] = e.Comment
		}
	}

	existing, err := app.Load(app.Options{ConfigPath: cfgPath, NoLocal: true})
	if err != nil {
		return "", err
	}
	existingProf, exists := existing.Profiles[name]

	extendsToWrite := config.Extends(nil)
	if extendsFlag != "" {
		for _, raw := range strings.Split(extendsFlag, ",") {
			p := strings.TrimSpace(raw)
			if p == "" {
				continue
			}
			if _, ok := existing.Profiles[p]; !ok {
				return "", fmt.Errorf("extends profile %q does not exist", p)
			}
			extendsToWrite = append(extendsToWrite, p)
		}
	} else if exists {
		extendsToWrite = existingProf.Extends
	}

	if len(imported) == 0 && len(extendsToWrite) == 0 {
		return "", fmt.Errorf("no variables to import")
	}

	oldEnv := map[string]string{}
	oldExtends := config.Extends(nil)
	if exists {
		oldEnv = existingProf.Env
		oldExtends = existingProf.Extends
	}
	d := computeImportDiff(oldEnv, imported)

	if exists && replace {
		d.removed = removedKeys(oldEnv, imported)
		if !force && len(d.removed) > 0 {
			ok, cerr := confirm(replaceConfirmMsg(name, d.removed), false)
			if cerr != nil {
				return "", fmt.Errorf("--replace would remove %d key(s) from %q; rerun with --force", len(d.removed), name)
			}
			if !ok {
				return "", nil
			}
		}
		if err := config.WriteProfile(cfgPath, name, config.Profile{Extends: extendsToWrite, Env: imported}, false, false, comments); err != nil {
			return "", err
		}
		return formatImportSummary(name, len(imported), "replaced", d, extendsToWrite, oldExtends), nil
	}
	if err := config.UpsertProfile(cfgPath, name, config.Profile{Extends: extendsToWrite, Env: imported}, false, false, comments); err != nil {
		return "", err
	}
	mode := "created"
	if exists {
		mode = "merge"
	}
	return formatImportSummary(name, len(imported), mode, d, extendsToWrite, oldExtends), nil
}

type diffEntry struct{ key, val string }

type importDiff struct{ added, overridden, removed []diffEntry }

// computeImportDiff classifies imported keys against the profile's existing env
// as added (absent) or overridden (present, different). Unchanged keys are
// omitted. Returned slices are sorted by key for stable output.
func computeImportDiff(oldEnv, imported map[string]string) importDiff {
	var d importDiff
	keys := make([]string, 0, len(imported))
	for k := range imported {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := imported[k]
		old, ok := oldEnv[k]
		if !ok {
			d.added = append(d.added, diffEntry{k, v})
		} else if old != v {
			d.overridden = append(d.overridden, diffEntry{k, v})
		}
	}
	return d
}

// removedKeys returns existing keys absent from imported, sorted by key, for the
// --replace path.
func removedKeys(oldEnv, imported map[string]string) []diffEntry {
	keys := make([]string, 0, len(oldEnv))
	for k := range oldEnv {
		if _, ok := imported[k]; !ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]diffEntry, 0, len(keys))
	for _, k := range keys {
		out = append(out, diffEntry{k, oldEnv[k]})
	}
	return out
}

// replaceConfirmMsg builds the --replace confirmation prompt: the number of keys
// to remove and their names (capped to the first five).
func replaceConfirmMsg(name string, removed []diffEntry) string {
	keys := make([]string, len(removed))
	for i, e := range removed {
		keys[i] = e.key
	}
	shown := keys
	tail := ""
	if len(keys) > 5 {
		shown = keys[:5]
		tail = fmt.Sprintf(", ... and %d more", len(keys)-5)
	}
	noun := "keys"
	if len(keys) == 1 {
		noun = "key"
	}
	return fmt.Sprintf("Replace will remove %d %s from %q: %s%s. Continue?", len(keys), noun, name, strings.Join(shown, ", "), tail)
}

func extLabel(e config.Extends) string {
	if len(e) == 0 {
		return "(none)"
	}
	return strings.Join(e, ", ")
}

// formatImportSummary renders the import result: a header line, then per-key diff
// lines (added +, overridden ~, removed -) with values masked, and an extends
// line when the value changed.
func formatImportSummary(name string, n int, mode string, d importDiff, extendsToWrite, oldExtends config.Extends) string {
	var b strings.Builder
	vars := "1 var"
	if n != 1 {
		vars = fmt.Sprintf("%d vars", n)
	}
	fmt.Fprintf(&b, "\n✓ imported %s into %q — %s\n", vars, name, mode)
	for _, e := range d.added {
		fmt.Fprintf(&b, "  + %s = %s\n", e.key, config.MaskValue(e.key, e.val))
	}
	for _, e := range d.overridden {
		fmt.Fprintf(&b, "  ~ %s = %s\n", e.key, config.MaskValue(e.key, e.val))
	}
	for _, e := range d.removed {
		fmt.Fprintf(&b, "  - %s = %s\n", e.key, config.MaskValue(e.key, e.val))
	}
	if !extendsEqual(extendsToWrite, oldExtends) {
		fmt.Fprintf(&b, "  extends: %s → %s\n", extLabel(oldExtends), extLabel(extendsToWrite))
	}
	fmt.Fprintf(&b, "Run `enver encrypt %s` to encrypt secrets.\n", name)
	return b.String()
}
