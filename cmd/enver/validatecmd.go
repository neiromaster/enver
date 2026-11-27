package main

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/remote"
	"github.com/spf13/cobra"
)

// validateStrict is the --strict flag: a warning is graded as a failure.
var validateStrict bool

var validateCmd = &cobra.Command{
	Use:           "validate [files...]",
	Short:         "Check config health, or named files by their own rules",
	Args:          cobra.ArbitraryArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		var paths []string
		if globalFlags.file != "" {
			paths = append(paths, globalFlags.file)
		}
		paths = append(paths, args...)
		if len(paths) == 0 {
			return validateMerged(cmd.OutOrStdout())
		}
		return runFileValidate(cmd.OutOrStdout(), paths, validateStrict)
	},
}

func init() {
	validateCmd.Flags().BoolVar(&validateStrict, "strict", false, "warnings fail the check")
}

// validateMerged is the no-files mode: the health of the merged config of the
// current directory. The checks are the merged view's own, so an extends edge
// that crosses layers is judged where it resolves.
func validateMerged(w io.Writer) error {
	cfg, err := app.Load(appOpts())
	if err != nil {
		return err
	}
	// Isolated-global catches a global profile extending a local-only name.
	globalIssues, err := config.ValidateGlobal(config.GlobalPath(globalFlags.configPath))
	if err != nil {
		return err
	}
	issues := dedupIssues(append(
		config.Validate(cfg),
		globalIssues...,
	))
	hasErr, hasWarn := false, false
	for _, is := range issues {
		switch is.Severity {
		case config.SeverityError:
			hasErr = true
		case config.SeverityWarning:
			hasWarn = true
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
		return errors.New("config has errors")
	}
	if validateStrict && hasWarn {
		return errors.New("config has warnings treated as errors (--strict)")
	}
	return nil
}

// fileVerdict is one named file's full outcome: the lines to print for it, and
// how it counts toward the run's summary.
type fileVerdict struct {
	lines  []string
	failed bool
	// skipped marks a file that is not an enver config: it counts toward
	// neither the checked nor the failed total.
	skipped bool
	// strictOnly marks a file whose worst findings are warnings, so only
	// --strict fails it. The summary names the flag as the cause through these
	// files alone.
	strictOnly bool
}

// checkFile checks one file's bytes by the rules its own content declares: the
// lines to print for it, and whether it failed — an error, or a warning under
// strict.
func checkFile(path string, data []byte, strict bool) (lines []string, failed bool) {
	v := checkFileVerdict(path, data, strict)
	return v.lines, v.failed
}

// checkFileVerdict is the full per-file check. The content picks its rule set:
// a publish mark is judged as the content a pull would fetch, an unmarked file
// with an enver section as its own local config, and a file that is neither is
// skipped. Findings render under the path, ordered errors, warnings, notes.
func checkFileVerdict(path string, data []byte, strict bool) fileVerdict {
	kind, unknownKeys, err := config.InspectFile(data)
	if err != nil {
		return fileVerdict{failed: true, lines: []string{fmt.Sprintf("%s: error: %v", path, err)}}
	}
	if kind == config.FileNotEnver {
		return fileVerdict{skipped: true, lines: []string{path + ": skipped: not an enver config"}}
	}
	mode := remote.Local
	if kind == config.FilePublish {
		mode = remote.Publish
	}
	var (
		errs, warns, notes []string
		hadErr, hadWarn    bool
	)
	emit := func(sev config.Severity, text string) {
		line := fmt.Sprintf("%s: %s: %s", path, sev, text)
		switch sev {
		case config.SeverityError:
			errs, hadErr = append(errs, line), true
		case config.SeverityWarning:
			warns, hadWarn = append(warns, line), true
		default:
			notes = append(notes, line)
		}
	}
	for _, key := range unknownKeys {
		emit(config.SeverityError, fmt.Sprintf("unknown top-level key %q", key))
	}
	cfg, findings, err := remote.Check(data, mode, "")
	if err != nil {
		emit(config.SeverityError, err.Error())
	} else {
		refused := false
		for _, f := range findings {
			refused = refused || f.Severity == config.SeverityError
			emit(f.Severity, f.Text)
		}
		for _, is := range config.ValidateOwn(cfg) {
			emit(config.SeverityWarning, is.String())
		}
		// A parse the check refused decodes to an empty view, which would read
		// as declaring nothing; only a completed check may say that.
		if kind == config.FilePublish && !refused && len(cfg.Profiles) == 0 && len(cfg.Remotes) == 0 {
			marked := config.Issue{Kind: config.KindMarkedEmpty, Severity: config.SeverityWarning}
			emit(config.SeverityWarning, marked.Reason())
		}
	}
	lines := slices.Concat(errs, warns, notes)
	if len(lines) == 0 {
		lines = []string{path + ": ✓ valid as a local config"}
		if kind == config.FilePublish {
			lines[0] = path + ": ✓ valid for publication"
		}
	}
	return fileVerdict{
		lines:      lines,
		failed:     hadErr || (strict && hadWarn),
		strictOnly: strict && hadWarn && !hadErr,
	}
}

// runFileValidate checks the named paths in order and prints each file's lines
// under the previous one's. A path nothing can be read from counts as checked
// and failed; a skipped file counts toward neither total. The returned error
// is the run's summary, so main exits 1 with it on stderr.
func runFileValidate(w io.Writer, paths []string, strict bool) error {
	checked, failed := 0, 0
	warnFailed := false
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			if _, err := fmt.Fprintf(w, "%s: error: cannot read: %v\n", path, err); err != nil {
				return err
			}
			checked++
			failed++
			continue
		}
		v := checkFileVerdict(path, data, strict)
		for _, line := range v.lines {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		if v.skipped {
			continue
		}
		checked++
		if v.failed {
			failed++
		}
		warnFailed = warnFailed || v.strictOnly
	}
	if failed == 0 {
		return nil
	}
	summary := fmt.Sprintf("%d of %d files failed", failed, checked)
	if warnFailed {
		summary += " (warnings treated as errors)"
	}
	return errors.New(summary)
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
