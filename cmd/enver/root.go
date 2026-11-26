// Enver injects environment variables from named, layered YAML profiles
// into any child command.
//
// Configuration is a two-layer merge: one global store at
// ~/.config/enver/config.yaml overlaid by an optional project file,
// ./.enver.yaml. Profiles inherit values through multi-parent extends
// chains resolved root-first, and unset fences keep listed keys out of
// the injected environment so shell values pass through untouched.
// Secret values are encrypted at rest with argon2id-derived keys and
// AES-256-GCM. enver never writes to the target tool's own
// configuration.
//
// # Commands
//
//   - x runs a child command with the resolved profile environment
//     injected, stdio connected, exit code propagated.
//   - show prints a resolved profile with per-value provenance and
//     masks secret values in text output.
//   - export prints shell export lines for a profile.
//   - list prints available profiles; validate reports config errors.
//   - add, edit, remove, rename, duplicate and default manage profiles.
//     add and edit always drive an interactive UI and need a TTY on
//     stdin; remove, rename, duplicate and default prompt only for
//     missing arguments and otherwise run non-interactively (remove
//     takes --yes to skip its confirmation).
//   - import converts a .env file into a profile; dotenv prints the
//     profile back in .env form.
//   - encrypt, decrypt and keygen manage encryption of secret values; a key
//     written with a leading ~ mark is an encrypt candidate whatever its
//     name, and the mark stays in the file across encrypt and decrypt.
//   - pull fetches a remote config, validates it, and stores it as the
//     lowest layer, so profiles can extend names another machine
//     publishes; nothing is fetched while a profile is read.
//   - update updates the enver binary to the latest release; --check only
//     reports whether an update is available.
//
// Full documentation lives at
// https://github.com/neiromaster/enver/tree/main/docs.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/neiromaster/enver/internal/version"
	"github.com/spf13/cobra"
)

// globalOptions is the root's persistent flags. It is a named type so tests
// can reset the process view with its zero value.
type globalOptions struct {
	configPath string
	keyPath    string
	noLocal    bool
	noExpand   bool
	global     bool
	chdir      string
	// file is the --file value exactly as typed: hints and the write target
	// spell it that way, while applyFile derives the absolute path from it.
	file string
}

var globalFlags globalOptions

var rootCmd = &cobra.Command{
	Use:   "enver",
	Short: "Inject environment variables from layered YAML profiles into a command",
	Long: `enver — environment profile injector.

Manage named, layered YAML profiles and run commands with a profile's
environment injected, without mutating any tool's own config.

  enver x [profiles...] -- <command>    run a command with composed profiles
  enver show [profiles...]              preview resolved env (masked)
  enver export [profiles...]            print ` + "`export K=V`" + ` for eval
  enver dotenv [profiles...]            export profiles to a .env file (with comments)
  enver import <file> [profile]         import a .env file into a profile
  enver list                            list profiles
  enver add [name]                      interactively add a profile
  enver edit [profile]                  edit a profile (vars, extends, default)
  enver remove [profile]                delete a profile
  enver rename [old] [new]              rename a profile (+ extends/default refs)
  enver duplicate <src> [new]           copy a profile
  enver default [profile]               set/show the default (--clear to clear)
  enver validate                        check config health
  enver pull <source> --as <name>       fetch and register a remote config
  enver update [--check]                update enver to the latest release
  enver keygen | encrypt | decrypt      manage encrypted secrets

Profile names may collide with subcommand verbs; enver x <name> -- <command>
addresses such a profile directly. Read commands accept several profiles:
enver x base slot -- cmd composes them left to right (later wins), as an
anonymous extends.

Pass --file <path> to work on that file as the whole config: no global or
.enver.yaml layer is read, writers write the file, and the content pulled for
it is stored in .enver/<file>/ beside it.

Config: $XDG_CONFIG_HOME/enver/config.yaml (default ~/.config/enver/config.yaml),
plus a local .enver.yaml in the current directory. Mutating commands write to
the local file by default; pass --global (-g) to write the user config.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.Version = version.String()

	pf := rootCmd.PersistentFlags()
	pf.StringVar(&globalFlags.configPath, "config", "", "override the global config file")
	pf.StringVar(&globalFlags.keyPath, "key", "", "key file (or ENVER_KEY env)")
	pf.BoolVar(&globalFlags.noLocal, "no-local", false, "ignore the ./.enver.yaml layer when reading")
	pf.BoolVar(&globalFlags.noExpand, "no-expand", false, "do not expand $VAR references")
	pf.BoolVarP(&globalFlags.global, "global", "g", false, "write to the global config instead of the local .enver.yaml (mutating commands)")
	pf.StringVar(&globalFlags.chdir, "chdir", "", "run as if started from this directory (.enver.yaml and relative --config resolve against it)")
	pf.StringVarP(&globalFlags.file, "file", "f", "", "work on this config file as the whole config (no global or ./.enver.yaml layer); content pulled for it is stored in .enver/<file> beside it")

	rootCmd.AddCommand(xCmd, showCmd, exportCmd, dotenvCmd, importCmd, listCmd, keygenCmd, encryptCmd, decryptCmd, addCmd, defaultCmd, validateCmd, removeCmd, renameCmd, duplicateCmd, editCmd, updateCmd, pullCmd)

	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		return applyGlobalFlags()
	}

	app.Interactive = ui.Interactive
	app.PromptPassphrase = ui.Password
	app.LoadWarnings = renderLoadWarnings
}

// loadWarningsOut is where the advisories a merged load collects are printed:
// stderr, so a command's stdout stays the machine-readable output it promises.
// A var so tests can capture the stream.
var loadWarningsOut io.Writer = os.Stderr

// renderLoadWarnings is the one sink for load-time advisories, wired to
// app.LoadWarnings: every read command, TUI entry, and the x path before it hands
// the process to the child reports them the same way, and none of them needs its
// own copy of the wiring.
func renderLoadWarnings(warnings []string) {
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(loadWarningsOut, "warning: %s\n", warning)
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		var ce *codedError
		if errors.As(err, &ce) {
			if !ce.silent {
				fmt.Fprintf(os.Stderr, "enver: %v\n", ce.err)
			}
			os.Exit(ce.code)
		}
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		os.Exit(1)
	}
}

func appOpts() app.Options {
	opts := app.Options{
		ConfigPath: globalFlags.configPath,
		KeyPath:    globalFlags.keyPath,
		NoLocal:    globalFlags.noLocal,
		NoExpand:   globalFlags.noExpand,
	}
	if globalFlags.file != "" {
		opts.ConfigPath = ""
		opts.NoLocal = false
		opts.FilePath = globalFlags.file
	}
	return opts
}

// applyChdir changes to --chdir before any command logic so that LocalPath and
// relative --config resolve in the target directory. applyGlobalFlags runs it
// first, so --file resolves against the new directory; tests call it directly.
func applyChdir() error {
	return app.Chdir(globalFlags.chdir)
}

// applyGlobalFlags applies the root's directory and file flags in one pass, so
// every entry point — PersistentPreRunE, the completion functions that bypass
// it, and tests — resolves them the same way.
func applyGlobalFlags() error {
	if err := applyChdir(); err != nil {
		return err
	}
	return applyFile()
}

// applyFile points the process at one config file, once, before any command
// logic: it redirects the remote store beside the file and spells the pull
// hint, so every load and write reads the file alone. Conflicts and unusable
// paths are refused here rather than re-checked per command.
func applyFile() error {
	if globalFlags.file == "" {
		return nil
	}
	for _, conflict := range []struct {
		set  bool
		flag string
	}{
		{globalFlags.configPath != "", "config"},
		{globalFlags.noLocal, "no-local"},
		{globalFlags.global, "global"},
	} {
		if conflict.set {
			return fmt.Errorf("--file cannot be combined with --%s: it is the whole config", conflict.flag)
		}
	}
	abs, err := filepath.Abs(globalFlags.file)
	if err != nil {
		return err
	}
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		return fmt.Errorf("--file %s is a directory; name a config file", abs)
	}
	if info, err := os.Stat(filepath.Dir(abs)); err != nil || !info.IsDir() {
		return fmt.Errorf("--file %s: parent directory does not exist", abs)
	}
	en := filepath.Join(filepath.Dir(abs), ".enver")
	if info, err := os.Stat(en); err == nil && !info.IsDir() {
		return fmt.Errorf("%s exists but is not a directory, so no content can be stored beside %s", en, abs)
	}
	store := config.FileStoreDir(abs)
	config.RemotesDir = func() string { return store }
	config.PullHintFlags = "--file " + hintPath(globalFlags.file)
	return nil
}

// hintPath spells a path inside a hint line: quoted only when it contains
// whitespace, so the common case stays bare.
func hintPath(p string) string {
	if strings.IndexFunc(p, unicode.IsSpace) >= 0 {
		return strconv.Quote(p)
	}
	return p
}

func requireInteractive(what string) error {
	if ui.Interactive() {
		return nil
	}
	return fmt.Errorf("%s required; pass it as an argument (stdin is not a terminal)", what)
}

func interactiveOnly(name string) error {
	if ui.Interactive() {
		return nil
	}
	return fmt.Errorf("%s is interactive; run it in a terminal", name)
}

// aborted prints the shared notice for a declined destructive confirm and
// returns the write error, so declining exits 0 instead of reading as a
// failure. Call it on the stream the command prints its notices to (stdout).
func aborted(w io.Writer) error {
	_, err := fmt.Fprintln(w, "\naborted")
	return err
}

// writeTarget is the file mutators write: the --file file under --file, local
// by default, global under --global.
func writeTarget() string {
	if globalFlags.file != "" {
		return globalFlags.file
	}
	if globalFlags.global {
		return config.GlobalPath(globalFlags.configPath)
	}
	return config.LocalPath()
}

// underLayers reads the stack a write sits on, nearest first: the global file
// under a local write, and every pulled remote below that. Under --file the
// file replaces the user layers, so only the remotes pulled for it remain. A
// remote never folds over a user layer, whichever layer declares its record, so
// the records come from the merged view the write's marks are answered against.
// The advisories about that content are the merged load's to report, which is
// why they are dropped here. A secret mark inherited from any of those layers
// cannot be guessed, so their read errors surface here rather than as a quietly
// unmarked key.
func underLayers(merged config.Config) ([]config.Layer, error) {
	remotes, _, err := config.RemoteLayers(merged.Remotes)
	if err != nil {
		return nil, err
	}
	under := make([]config.Layer, 0, len(remotes)+1)
	if globalFlags.file == "" && !globalFlags.global {
		global, err := config.LoadFile(config.GlobalPath(globalFlags.configPath))
		if err != nil {
			return nil, err
		}
		under = append(under, config.Layer{Label: config.LayerGlobal, Config: global})
	}
	// RemoteLayers folds in sorted record order, so the last one is the nearest
	// layer under the target.
	for i := len(remotes) - 1; i >= 0; i-- {
		under = append(under, remotes[i])
	}
	return under, nil
}

// loadTarget reads the file mutators rewrite: local by default, global under
// --global. Existence is judged against this file, never the merged view — a
// profile visible through extends but absent here is not a write target.
func loadTarget() (path string, cfg config.Config, err error) {
	path = writeTarget()
	cfg, err = config.LoadFile(path)
	return path, cfg, err
}

// requireTargetProfile reports notFound when the write target has no profile
// named name, keeping the cross-layer hint in one place.
func requireTargetProfile(cfg config.Config, path, name string) error {
	if _, ok := cfg.Profiles[name]; !ok {
		return notFoundInTarget(name, path)
	}
	return nil
}

// requireWriteTargetProfile resolves the file encrypt and decrypt rewrite and
// gates a named profile on the target declaring it: sealing or opening values
// happens in the user's own copy, so a name only remote content provides is a
// miss the scope hint answers. An unnamed run (encrypt --all) covers whatever
// the target holds.
func requireWriteTargetProfile(profile string) (string, error) {
	path, cfg, err := loadTarget()
	if err != nil {
		return "", err
	}
	if profile == "" {
		return path, nil
	}
	return path, requireTargetProfile(cfg, path, profile)
}

// pickerConfig is the extends-picker set: merged by default (a local profile may
// extend a global one), global-only under --global.
func pickerConfig(merged config.Config) (config.Config, error) {
	if globalFlags.global {
		return config.LoadFile(config.GlobalPath(globalFlags.configPath))
	}
	return merged, nil
}

// notFoundInTarget hints at --global when the profile lives in the other layer,
// and at `add` when it lives only in remote content: remote profiles are a
// read-only base, so the writer's remedy is a patch copy in the user layer.
// Under --file there is no other user layer to point at, so only the remote
// hint remains.
func notFoundInTarget(name, target string) error {
	if globalFlags.file == "" {
		other := config.LocalPath()
		if target == other {
			other = config.GlobalPath(globalFlags.configPath)
		}
		if c, err := config.LoadFile(other); err == nil {
			if _, ok := c.Profiles[name]; ok {
				if globalFlags.global {
					return fmt.Errorf("profile %q not found in global config; it is local — run without --global", name)
				}
				return fmt.Errorf("profile %q not found in local .enver.yaml; it is global — pass --global (-g)", name)
			}
		}
	}
	// A merged load that fails here would only replace a message the caller
	// already knows is a miss, so the plain not-found answer stands.
	if merged, err := app.Load(appOpts()); err == nil {
		if remote, ok := config.RemoteName(merged.ProfileLayer[name]); ok {
			return fmt.Errorf("profile %q comes from remote %q and has no copy here; add a patch copy first (`%s`)", name, remote, addCommandLine(name))
		}
	}
	return fmt.Errorf("profile %q not found", name)
}

// addCommandLine is the command a scope hint points at: the patch copy lands in
// the write target, so it needs the same scope flag the refused write carried.
func addCommandLine(name string) string {
	if globalFlags.file != "" {
		return "enver add --file " + hintPath(globalFlags.file) + " " + name
	}
	if globalFlags.global {
		return "enver add -g " + name
	}
	return "enver add " + name
}

func targetProfiles(cmd *cobra.Command, toComplete string) []string {
	if err := applyGlobalFlags(); err != nil {
		return nil
	}
	cfg, err := completionTarget(cmd)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Profiles))
	for _, n := range cfg.ProfileNames() {
		if strings.HasPrefix(n, toComplete) {
			out = append(out, n)
		}
	}
	return out
}

// completionTarget is the config completion offers profile names from: the
// standalone file under --file, the local file by default, the global one
// under --global.
func completionTarget(cmd *cobra.Command) (config.Config, error) {
	if file, _ := cmd.Flags().GetString("file"); file != "" {
		return config.LoadStandalone(file)
	}
	g, _ := cmd.Flags().GetBool("global")
	cfgPath, _ := cmd.Flags().GetString("config")
	if g {
		return config.LoadFile(config.GlobalPath(cfgPath))
	}
	return config.LoadFile(config.LocalPath())
}

// osArgs is the seam completion tests use to fake the raw __complete line.
var osArgs = os.Args

// rawCompleteWords returns the raw words the shell passed to the __complete
// invocation (everything after the __complete/__completeNoDesc token,
// including the word being completed), or nil when this process is not a
// completion request. ValidArgsFunction sees cobra's post-parse argument
// view, where a typed `--` is invisible: the probe parse that runs before
// the real one leaves ArgsLenAtDash pointing past the typed line either way.
// The raw line is the only honest source for whether the cursor sits after
// the dash.
func rawCompleteWords() []string {
	for i, arg := range osArgs {
		if arg == "__complete" || arg == "__completeNoDesc" {
			return osArgs[i+1:]
		}
	}
	return nil
}

// completeProfile backs read commands: merged-config completion. Every
// positional before `--` is a member of the run-line composition, so each
// such position offers the merged profile names minus those already typed;
// tokens after `--` belong to the child command (enver x) and fall back to
// file completion. Completion is a silent read: the candidates are the whole
// output, so a load warning would be injected into the shell's own stream.
func completeProfile(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if err := applyGlobalFlags(); err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	raw := rawCompleteWords()
	if raw != nil {
		for _, w := range raw[:len(raw)-1] {
			if w == "--" {
				return nil, cobra.ShellCompDirectiveDefault
			}
		}
	}
	cfgPath, _ := cmd.Flags().GetString("config")
	noLocal, _ := cmd.Flags().GetBool("no-local")
	file, _ := cmd.Flags().GetString("file")
	names := app.MatchingProfiles(app.Options{ConfigPath: cfgPath, NoLocal: noLocal, FilePath: file}, toComplete)
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !slices.Contains(args, n) {
			out = append(out, n)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeProfileInTarget backs mutators: write-target-scoped completion.
func completeProfileInTarget(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}
	return targetProfiles(cmd, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// codedError carries a process exit code out of a command. main unwraps it:
// a silent error exits with code without printing; others print the message
// first. Without one, main exits 1.
type codedError struct {
	err    error
	code   int
	silent bool
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

// exitErr wraps err as an exit-code-2 failure for main.
func exitErr(err error, code int) error {
	return &codedError{err: err, code: code}
}
