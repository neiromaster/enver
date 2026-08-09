// Package app holds the shared run and resolve logic used by both the enver
// and enverx binaries.
package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/neiromaster/enver/internal/runner"
	"github.com/neiromaster/enver/internal/varsubst"
)

// Options carries the flags shared by every enver/enverx entry point.
type Options struct {
	ConfigPath string
	KeyPath    string
	NoLocal    bool
	NoExpand   bool   // skip $VAR interpolation
	Name       string // invocation label ("enverx" / "enver x") for error messages
}

// Load resolves the global config plus any local .enver.yaml layers.
func Load(opts Options) (config.Config, error) {
	return config.LoadMerged(opts.ConfigPath, !opts.NoLocal)
}

// Resolve walks the profile's extends chain and transparently decrypts any
// enc:v1: values. A key is required only when encrypted values are present.
func Resolve(cfg config.Config, profile string, opts Options) (map[string]string, []string, error) {
	env, chain, err := cfg.ResolveProfile(profile)
	if err != nil {
		return nil, chain, err
	}
	if !hasEncrypted(env) {
		if !opts.NoExpand {
			env = varsubst.Expand(env, osEnvMap())
		}
		return env, chain, nil
	}
	key, err := ResolveKey(opts)
	if err != nil {
		return nil, chain, err
	}
	if key == nil {
		return nil, chain, fmt.Errorf("encrypted values present but no key found; run `enver keygen` or set --key/ENVER_KEY")
	}
	for k, v := range env {
		if crypto.IsEncrypted(v) {
			plain, err := crypto.DecryptValue(v, key)
			if err != nil {
				return nil, chain, fmt.Errorf("decrypt %s: %w", k, err)
			}
			env[k] = plain
		}
	}
	if !opts.NoExpand {
		env = varsubst.Expand(env, osEnvMap())
	}
	return env, chain, nil
}

// Run is the full run path: parse args, load config, resolve the profile, and
// exec the child command with the merged environment. The child's non-zero exit
// code is propagated via os.Exit (cobra's RunE only signals 0/1).
func Run(args []string, dashAt int, opts Options) error {
	profile, cmdArgs := ParseProfileAndCmd(args, dashAt)
	if len(cmdArgs) == 0 {
		return fmt.Errorf("%s requires a command after `--`", opts.Name)
	}
	cfg, err := Load(opts)
	if err != nil {
		return err
	}
	if profile == "" {
		profile = cfg.Default
	}
	env, _, err := Resolve(cfg, profile, opts)
	if err != nil {
		return err
	}
	if code := runner.Run(cmdArgs, runner.MergedEnv(env)); code != 0 {
		os.Exit(code)
	}
	return nil
}

// ParseProfileAndCmd splits args at the `--` separator (cobra's ArgsLenAtDash).
// The first token before the dash is the profile; everything after is the child
// command and its own args.
func ParseProfileAndCmd(args []string, dashAt int) (profile string, cmdArgs []string) {
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

// ResolveKey resolves the decryption key from --key, the ENVER_KEY env var, or
// the default key file. It returns (nil, nil) when no key is available; callers
// decide whether that is an error.
func ResolveKey(opts Options) ([]byte, error) {
	if opts.KeyPath != "" {
		return crypto.LoadKey(opts.KeyPath)
	}
	if v := os.Getenv("ENVER_KEY"); v != "" {
		return crypto.DecodeKey(v)
	}
	if path := crypto.KeyFilePath(); fileExists(path) {
		return crypto.LoadKey(path)
	}
	return nil, nil
}

// MatchingProfiles returns profile names beginning with toComplete, for shell
// completion. CLI wrappers map the result onto their framework's directives
// (cobra ShellCompDirective), keeping this package free of cobra.
func MatchingProfiles(opts Options, toComplete string) []string {
	cfg, err := Load(opts)
	if err != nil {
		return nil
	}
	names := cfg.ProfileNames()
	out := make([]string, 0, len(names))
	for _, n := range names {
		if hasPrefix(n, toComplete) {
			out = append(out, n)
		}
	}
	return out
}

func hasEncrypted(env map[string]string) bool {
	for _, v := range env {
		if crypto.IsEncrypted(v) {
			return true
		}
	}
	return false
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// osEnvMap snapshots the process environment as a map for varsubst.Expand.
func osEnvMap() map[string]string {
	m := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}
