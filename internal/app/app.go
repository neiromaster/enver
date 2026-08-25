// Package app holds the shared run and resolve logic behind the enver
// commands.
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

// Options carries the flags shared by the enver commands.
type Options struct {
	ConfigPath string
	KeyPath    string
	NoLocal    bool
	NoExpand   bool   // skip $VAR interpolation
	Name       string // invocation label ("enver x") for error messages
}

// Interactive reports whether stdin is a terminal. Injected by the CLI layer
// (cmd/enver sets it to ui.Interactive) so this package stays free of ui.
var Interactive = func() bool { return false }

// PromptPassphrase reads a hidden passphrase. Injected by the CLI layer
// (cmd/enver sets it to ui.Password).
var PromptPassphrase func(prompt string) (string, error)

// Load resolves the global config plus any local .enver.yaml layers.
func Load(opts Options) (config.Config, error) {
	return config.LoadMerged(opts.ConfigPath, !opts.NoLocal)
}

// Chdir changes to dir when non-empty. Wired into PersistentPreRunE and the
// completion handlers, which bypass it; "" is a no-op.
func Chdir(dir string) error {
	if dir == "" {
		return nil
	}
	return os.Chdir(dir)
}

// Resolve walks the profile's extends chain, transparently decrypts any
// enc:v2: values, and expands $VAR interpolation unless opts.NoExpand. A key
// is required only when encrypted values are present. Comments ride through
// untouched.
func Resolve(cfg config.Config, profile string, opts Options) (config.Resolved, error) {
	r, err := cfg.ResolveProfile(profile)
	if err != nil {
		return config.Resolved{}, err
	}
	if !hasEncrypted(r.Env) {
		if !opts.NoExpand {
			r.Env = varsubst.Expand(r.Env, osEnvMap())
		}
		return r, nil
	}
	key, _, err := ResolveKeyOrPrompt(opts, func() ([]byte, string, error) {
		salt, sample := firstSaltAndSample(r.Env)
		return salt, sample, nil
	})
	if err != nil {
		return config.Resolved{}, err
	}
	for k, v := range r.Env {
		if crypto.IsEncrypted(v) {
			plain, err := crypto.DecryptValue(v, key)
			if err != nil {
				return config.Resolved{}, fmt.Errorf("decrypt %s: %w", k, err)
			}
			r.Env[k] = plain
		}
	}
	if !opts.NoExpand {
		r.Env = varsubst.Expand(r.Env, osEnvMap())
	}
	return r, nil
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
	profile, err = ProfileOrDefault(profile, cfg.Default)
	if err != nil {
		return err
	}
	r, err := Resolve(cfg, profile, opts)
	if err != nil {
		return err
	}
	if code := runner.Run(cmdArgs, runner.MergedEnv(osEnvMap(), r.Env), opts.Name, profile); code != 0 {
		os.Exit(code)
	}
	return nil
}

// ProfileOrDefault applies the config default to an empty profile name and
// errors when no profile is selectable. Centralizes the guard shared by Run,
// show, export, and dotenv so the fallback and message stay consistent.
func ProfileOrDefault(profile, def string) (string, error) {
	if profile == "" {
		profile = def
	}
	if profile == "" {
		return "", fmt.Errorf("no profile specified and no default set in config")
	}
	return profile, nil
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

// SaltSource locates the salt and a full sample value of the first enc:v2:
// value, for passphrase recovery when no key is configured. A nil salt means
// the source holds no encrypted value. Called only on the no-key path, so
// expensive sources stay idle when a key resolves.
type SaltSource func() (salt []byte, sample string, err error)

// ResolveKeyOrPrompt resolves the decryption key from --key, the ENVER_KEY env
// var, or the default key file, recovering it from a passphrase via saltSource
// when none is available. salt is nil only for ENVER_KEY keys, which can
// decrypt (the salt is embedded in each enc:v2: value) but not encrypt.
func ResolveKeyOrPrompt(opts Options, saltSource SaltSource) (key, salt []byte, err error) {
	key, salt, err = resolveKey(opts)
	if err != nil || key != nil {
		return key, salt, err
	}
	salt, sample, err := saltSource()
	if err != nil {
		return nil, nil, err
	}
	if salt == nil {
		return nil, nil, fmt.Errorf("no key found; run `enver keygen` or set --key/ENVER_KEY")
	}
	key, err = recoverKey(salt, sample)
	if err != nil {
		return nil, nil, err
	}
	return key, salt, nil
}

// resolveKey resolves the decryption key from --key, the ENVER_KEY env var, or
// the default key file. It returns (nil, nil, nil) when no key is available;
// callers decide whether that is an error.
func resolveKey(opts Options) (key, salt []byte, err error) {
	if opts.KeyPath != "" {
		return crypto.LoadKey(opts.KeyPath)
	}
	if v := os.Getenv("ENVER_KEY"); v != "" {
		k, err := crypto.DecodeKey(v)
		return k, nil, err
	}
	if path := crypto.KeyFilePath(); fileExists(path) {
		return crypto.LoadKey(path)
	}
	return nil, nil, nil
}

// recoverKey prompts for a passphrase, derives the key from salt, verifies it
// by decrypting sample, and writes the key cache. Returns the derived key.
func recoverKey(salt []byte, sample string) ([]byte, error) {
	if !Interactive() {
		return nil, fmt.Errorf("encrypted values present but no key found; run `enver keygen` or set --key/ENVER_KEY")
	}
	if PromptPassphrase == nil {
		return nil, fmt.Errorf("passphrase recovery is not configured")
	}
	pass, err := PromptPassphrase("Enter passphrase:")
	if err != nil {
		return nil, err
	}
	key, err := crypto.DeriveKey(pass, salt, crypto.CurrentParams)
	if err != nil {
		return nil, err
	}
	if _, err := crypto.DecryptValue(sample, key); err != nil {
		return nil, fmt.Errorf("wrong passphrase")
	}
	if err := crypto.WriteKeyCache(crypto.KeyFilePath(), crypto.NewKeyCache(salt, key)); err != nil {
		return nil, err
	}
	return key, nil
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

// osEnvMap snapshots the process environment as a map for varsubst.Expand and
// runner.MergedEnv.
func osEnvMap() map[string]string {
	m := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// firstSaltAndSample returns the salt and full value of the first enc:v2: value
// in env, or (nil, "") when there is none.
func firstSaltAndSample(env map[string]string) ([]byte, string) {
	for _, v := range env {
		if s, _, err := crypto.SaltFromValue(v); err == nil {
			return s, v
		}
	}
	return nil, ""
}
