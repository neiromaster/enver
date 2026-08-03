package main

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

const version = "0.1.0"

const helpText = `enver — environment profile injector (v%s)

Inject environment variables from a layered YAML config into a child command,
without mutating any tool's own config.

Usage:
  enver [profile] -- <command> [args...]      Run command with the profile's env
  enver [profile]                             Show the profile's resolved env (masked)
  enver [profile] --print                     Same as above, explicit
  enver [profile] --export                    Print "export K=V" lines (unmasked, for eval)
  enver -l, --list                            List profiles
  enver -h, --help                            Show this help
  enver -v, --version                         Show version

Options:
  --config <path>     Override the global config file
  --no-local          Ignore .enver.yaml files in the directory hierarchy
  --no-mask           Show full secret values with --print

Config locations (merged in order, later wins):
  1. $XDG_CONFIG_HOME/enver/config.yaml  (or ~/.config/enver/config.yaml)
  2. .enver.yaml walked from cwd up to (not including) $HOME

When no profile is given, the config's ` + "`default`" + ` is used.

Examples:
  enver anth -- claude                       # run claude with the "anth" profile
  enver -- claude                            # run claude with the default profile
  enver openrouter -- claude --model claude-sonnet-5
  enver anth                                 # preview resolved env (masked)
  eval "$(enver anth --export)"              # apply to current shell
`

type opts struct {
	profile    string
	cmdArgs    []string
	printMode  bool
	exportMode bool
	listMode   bool
	noMask     bool
	noLocal    bool
	configPath string
	help       bool
	showVer    bool
}

func parseArgs(args []string) (opts, error) {
	o := opts{}
	sawSep := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if sawSep {
			o.cmdArgs = append(o.cmdArgs, a)
			continue
		}
		switch {
		case a == "--":
			sawSep = true
		case a == "-l" || a == "--list":
			o.listMode = true
		case a == "--print":
			o.printMode = true
		case a == "--export":
			o.exportMode = true
		case a == "--no-mask":
			o.noMask = true
		case a == "--no-local":
			o.noLocal = true
		case a == "-h" || a == "--help":
			o.help = true
		case a == "-v" || a == "--version":
			o.showVer = true
		case a == "--config":
			if i+1 >= len(args) {
				return o, fmt.Errorf("--config requires a value")
			}
			o.configPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--config="):
			o.configPath = strings.TrimPrefix(a, "--config=")
		case strings.HasPrefix(a, "-") && len(a) > 1:
			return o, fmt.Errorf("unknown flag %q", a)
		default:
			if o.profile == "" {
				o.profile = a
			} else {
				return o, fmt.Errorf("unexpected argument %q (profile already set to %q)", a, o.profile)
			}
		}
	}
	return o, nil
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	o, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		fmt.Fprintln(os.Stderr, "run `enver --help` for usage")
		return 2
	}
	if o.help {
		fmt.Printf(helpText, version)
		return 0
	}
	if o.showVer {
		fmt.Println("enver", version)
		return 0
	}

	cfg, err := loadMerged(o.configPath, !o.noLocal)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		return 2
	}

	if o.listMode {
		return doList(cfg)
	}

	// Decide run vs. preview.
	hasCmd := len(o.cmdArgs) > 0
	profileExplicit := o.profile != ""
	profile := o.profile
	if profile == "" {
		profile = cfg.Default
	}

	if !hasCmd && !o.printMode && !o.exportMode {
		// Bare invocation: preview a profile if one was named, else list.
		if profileExplicit {
			return doPrint(cfg, profile, false, false)
		}
		return doList(cfg)
	}

	if profile == "" {
		fmt.Fprintln(os.Stderr, "enver: no profile specified and no `default` set in config")
		return 2
	}

	if o.exportMode {
		return doPrint(cfg, profile, true, true)
	}
	if o.printMode {
		return doPrint(cfg, profile, false, o.noMask)
	}

	// run mode
	env, _, err := cfg.resolveProfile(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		return 2
	}
	return runCmd(o.cmdArgs, mergedEnv(env))
}

// mergedEnv starts from the current environment and overlays the profile vars,
// returning a sorted "K=V" slice suitable for exec.Cmd.Env.
func mergedEnv(profileEnv map[string]string) []string {
	curMap := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			curMap[k] = v
		}
	}
	for k, v := range profileEnv {
		curMap[k] = v
	}
	keys := make([]string, 0, len(curMap))
	for k := range curMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	res := make([]string, 0, len(keys))
	for _, k := range keys {
		res = append(res, k+"="+curMap[k])
	}
	return res
}

func runCmd(cmdArgs []string, env []string) int {
	path, err := exec.LookPath(cmdArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "enver: command not found: %s\n", cmdArgs[0])
		return 127
	}
	cmd := exec.Command(path, cmdArgs[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		return 1
	}
	return 0
}

func doList(cfg Config) int {
	names := cfg.profileNames()
	if len(names) == 0 {
		fmt.Println("(no profiles defined)")
		fmt.Printf("\nCreate one at: %s\n", globalConfigPath(""))
		return 0
	}
	fmt.Printf("%-4s %-20s %-16s %s\n", "", "PROFILE", "EXTENDS", "VARS")
	for _, n := range names {
		p := cfg.Profiles[n]
		marker := " "
		if n == cfg.Default {
			marker = "*"
		}
		extends := p.Extends
		if extends == "" {
			extends = "-"
		}
		fmt.Printf("%-4s %-20s %-16s %d\n", marker, n, extends, len(p.Env))
	}
	fmt.Println("\n* = default")
	return 0
}

func doPrint(cfg Config, profile string, exportFmt, unmasked bool) int {
	env, chain, err := cfg.resolveProfile(profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		return 2
	}
	if !exportFmt {
		fmt.Printf("# profile: %s\n", strings.Join(chain, " → "))
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := env[k]
		if exportFmt {
			fmt.Printf("export %s=%s\n", k, shellQuote(v))
			continue
		}
		if !unmasked {
			v = maskValue(k, v)
		}
		fmt.Printf("%s=%s\n", k, v)
	}
	return 0
}

// shellQuote wraps a value in single quotes, escaping embedded single quotes,
// so the output of --export is safe for POSIX shells.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}