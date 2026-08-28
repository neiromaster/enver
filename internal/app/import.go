package app

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/dotenv"
	"github.com/neiromaster/enver/internal/envname"
)

// ImportOptions carries the import knobs the CLI layer binds flags to.
// Confirm gates destructive --replace removals; Resolve computes the
// post-write effective resolution for fence reporting (nil disables it,
// tests).
type ImportOptions struct {
	Replace bool
	Force   bool
	Confirm func(msg string, def bool) (bool, error)
	Resolve func(profile string) (config.Resolved, error)
}

// ImportEnv parses .env data from r into profile name at target. Imported
// keys override existing same-named keys (merge); Replace wipes the profile's
// own env and unset list first, so an imported key the old profile fenced
// survives the import. The extends value is preserved unless extendsFlag is
// non-empty, in which case it is set; parents and cycles are validated by the
// caller against the merged view. Returns a one-line summary.
func ImportEnv(r io.Reader, target, name, extendsFlag string, opts ImportOptions) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	entries, skips, err := dotenv.Parse(data)
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

	existing, err := config.LoadFile(target)
	if err != nil {
		return "", err
	}
	existingProf, exists := existing.Profiles[name]

	extendsToWrite := config.Extends(nil)
	if extendsFlag != "" {
		extendsToWrite = SplitExtends(extendsFlag)
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

	var mode string
	if exists && opts.Replace {
		d.removed = removedKeys(oldEnv, imported)
		oldUnset := existingProf.Unset
		if !opts.Force && (len(d.removed) > 0 || len(oldUnset) > 0) {
			ok, cerr := opts.Confirm(replaceConfirmMsg(name, d.removed, oldUnset), false)
			if cerr != nil {
				return "", fmt.Errorf("--replace would remove %d key(s) and clear %d unset entries from %q; rerun with --force", len(d.removed), len(oldUnset), name)
			}
			if !ok {
				return "", nil
			}
		}
		// Unset stays nil: --replace clears the unset list like the profile's
		// own env, because WriteProfile writes the field wholesale.
		if err := config.WriteProfile(target, name, config.Profile{Extends: extendsToWrite, Unset: nil, Env: imported, Comments: comments}, false, false); err != nil {
			return "", err
		}
		mode = "replaced"
	} else {
		if err := config.UpsertProfile(target, name, config.Profile{Extends: extendsToWrite, Env: imported, Comments: comments}, false, false); err != nil {
			return "", err
		}
		mode = "created"
		if exists {
			mode = "merge"
		}
	}
	summary := formatImportSummary(name, len(imported), mode, d, extendsToWrite, oldExtends, fencedImportedKeys(opts.Resolve, name, imported))
	return summary + skippedLineNote(skips), nil
}

// SplitExtends parses a comma-separated --extends value, dropping empties.
func SplitExtends(raw string) config.Extends {
	var out config.Extends
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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
	slices.Sort(keys)
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
	slices.Sort(keys)
	out := make([]diffEntry, 0, len(keys))
	for _, k := range keys {
		out = append(out, diffEntry{k, oldEnv[k]})
	}
	return out
}

// fencedImportedKeys reports which of the imported keys the effective
// resolution strips: written to the file, but fenced by the profile's own
// unset — dead on arrival at every consumption point. An inherited unset does
// not fence a closer redefinition, so only the profile's own fence can drop
// an imported key from the resolved env. A nil resolve (tests) or an
// unresolvable profile (validate reports it) disables the reporting. Matching
// follows envname.Equal, as resolution does.
func fencedImportedKeys(resolve func(string) (config.Resolved, error), name string, imported map[string]string) map[string]bool {
	if resolve == nil {
		return nil
	}
	r, err := resolve(name)
	if err != nil {
		return nil
	}
	var out map[string]bool
	for k := range imported {
		if !envname.Has(r.Env, k) {
			if out == nil {
				out = map[string]bool{}
			}
			out[k] = true
		}
	}
	return out
}

// replaceConfirmMsg builds the --replace confirmation prompt: the keys to
// remove (capped to the first five) and the unset entries the wipe clears —
// fenced keys come back, so the fence is part of what is destroyed.
func replaceConfirmMsg(name string, removed []diffEntry, unset []string) string {
	var b strings.Builder
	if len(removed) > 0 {
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
		fmt.Fprintf(&b, "remove %d %s from %q: %s%s", len(keys), noun, name, strings.Join(shown, ", "), tail)
	}
	if len(unset) > 0 {
		if b.Len() > 0 {
			b.WriteString(" and ")
		}
		shown := unset
		tail := ""
		if len(unset) > 5 {
			shown = unset[:5]
			tail = fmt.Sprintf(", ... and %d more", len(unset)-5)
		}
		fmt.Fprintf(&b, "clear the unset list of %q: %s%s (fenced keys come back)", name, strings.Join(shown, ", "), tail)
	}
	return "Replace will " + b.String() + ". Continue?"
}

func extLabel(e config.Extends) string {
	if len(e) == 0 {
		return "(none)"
	}
	return strings.Join(e, ", ")
}

// formatImportSummary renders the import result: a header line, then per-key diff
// lines (added +, overridden ~, removed -) echoing the values verbatim, and an
// extends line when the value changed. The data came from the user's own .env,
// so masking would hide exactly what the diff is meant to confirm.
func formatImportSummary(name string, n int, mode string, d importDiff, extendsToWrite, oldExtends config.Extends, fenced map[string]bool) string {
	var b strings.Builder
	vars := "1 var"
	if n != 1 {
		vars = fmt.Sprintf("%d vars", n)
	}
	fmt.Fprintf(&b, "\n✓ imported %s into %q — %s\n", vars, name, mode)
	for _, e := range d.added {
		if fenced[e.key] {
			fmt.Fprintf(&b, "  ! %s = %s — fenced by unset; never reaches the resolved env\n", e.key, e.val)
			continue
		}
		fmt.Fprintf(&b, "  + %s = %s\n", e.key, e.val)
	}
	for _, e := range d.overridden {
		if fenced[e.key] {
			fmt.Fprintf(&b, "  ! %s = %s — fenced by unset; never reaches the resolved env\n", e.key, e.val)
			continue
		}
		fmt.Fprintf(&b, "  ~ %s = %s\n", e.key, e.val)
	}
	for _, e := range d.removed {
		fmt.Fprintf(&b, "  - %s = %s\n", e.key, e.val)
	}
	if !slices.Equal(extendsToWrite, oldExtends) {
		fmt.Fprintf(&b, "  extends: %s → %s\n", extLabel(oldExtends), extLabel(extendsToWrite))
	}
	fmt.Fprintf(&b, "Run `enver encrypt %s` to encrypt secrets.\n", name)
	return b.String()
}

// skippedLineNote renders the skipped-line appendix for the import summary,
// listing at most 3 lines before folding the rest into a count.
func skippedLineNote(skips []dotenv.Skip) string {
	if len(skips) == 0 {
		return ""
	}
	var parts []string
	for i, s := range skips {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("… %d more", len(skips)-3))
			break
		}
		parts = append(parts, fmt.Sprintf("line %d (%s)", s.Line, s.Reason))
	}
	noun := "lines"
	if len(skips) == 1 {
		noun = "line"
	}
	return fmt.Sprintf("\nskipped %d %s: %s\n", len(skips), noun, strings.Join(parts, ", "))
}
