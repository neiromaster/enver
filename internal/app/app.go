// Package app holds the shared run and resolve logic behind the enver
// commands.
package app

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/neiromaster/enver/internal/envname"
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
	// FilePath names one config file to work on as the whole config: reads come
	// from it and the content pulled for it, and no user layer contributes.
	// Setting it makes ConfigPath and NoLocal meaningless.
	FilePath string
	// TolerateMissingKey degrades an absent key to a per-key report instead of
	// a failure. Only the text display path may set it: a child process or a
	// machine consumer started with unreadable values fails somewhere else.
	TolerateMissingKey bool
	// TolerateRemoteWrongKey degrades a decrypt failure on a value a remote
	// supplied to a per-key report instead of failing the whole profile: such
	// ciphertext most likely belongs to its author's key, so it is a value to
	// patch rather than a key mismatch about the user's own file. Only a display
	// path may set it; the exec and export paths refuse instead.
	TolerateRemoteWrongKey bool
}

// ErrNoKey reports that a key is needed and none was found: encrypted values
// are in play, but no --key, ENVER_KEY, or key file resolved one, and a
// passphrase cannot be recovered without a prompt. It is the absence of a key,
// not an unusable one — a wrong key is a misconfiguration and never matches.
// Callers add the keys that stayed unread (WithEncryptedKeys) so the failure
// says what it blocked.
var ErrNoKey = errors.New("no key found — run `enver keygen` or set --key/ENVER_KEY")

// WithEncryptedKeys annotates a missing-key error with the sorted names of the
// values that stayed unread. Any other error, and an empty key list, pass
// through unchanged; the annotated result still matches ErrNoKey via errors.Is.
func WithEncryptedKeys(err error, keys []string) error {
	if !errors.Is(err, ErrNoKey) || len(keys) == 0 {
		return err
	}
	return fmt.Errorf("%w (encrypted: %s)", err, strings.Join(slices.Sorted(slices.Values(keys)), ", "))
}

// noKeyReason is the short per-key explanation the display paths render.
const noKeyReason = "no key — run enver keygen"

// wrongKeyReason is the per-key explanation the display paths render for remote
// ciphertext the configured key cannot read.
const wrongKeyReason = "wrong key (remote)"

// Interactive reports whether stdin is a terminal. Injected by the CLI layer
// (cmd/enver sets it to ui.Interactive) so this package stays free of ui.
var Interactive = func() bool { return false }

// PromptPassphrase reads a hidden passphrase. Injected by the CLI layer
// (cmd/enver sets it to ui.Password).
var PromptPassphrase func(prompt string) (string, error)

// LoadWarnings receives the advisories a merged load collects: content pulled
// from a source its record no longer points at, content no record declares, and
// bare-name fold collisions. The CLI wires it to stderr; a nil sink drops them,
// which is what a shell completion wants.
var LoadWarnings func(warnings []string)

// Load resolves the global config plus any local .enver.yaml layers, and reports
// the advisories the merged view raises.
func Load(opts Options) (config.Config, error) {
	return loadMerged(opts, true)
}

// loadMerged is the one merged load, so a path that reads the config either
// reports the advisories or deliberately stays silent — there is no third
// behavior to keep in sync.
func loadMerged(opts Options, report bool) (config.Config, error) {
	var cfg config.Config
	var err error
	if opts.FilePath != "" {
		cfg, err = config.LoadStandalone(opts.FilePath)
	} else {
		cfg, err = config.LoadMerged(opts.ConfigPath, !opts.NoLocal)
	}
	if report && LoadWarnings != nil {
		LoadWarnings(cfg.Warnings)
	}
	return cfg, err
}

// ResolveProfiles is the read-side trio: load the merged config, apply the
// default to an empty profile list, resolve the composition. Every consuming
// command shares this sequence. The returned label is the post-default list
// joined with "+" ("a+b"; a lone profile stays a bare name), which JSON
// output and the exec title still need.
func ResolveProfiles(opts Options, profiles []string) (string, config.Resolved, error) {
	cfg, err := Load(opts)
	if err != nil {
		return "", config.Resolved{}, err
	}
	profiles, err = profilesOrDefault(profiles, cfg.Default)
	if err != nil {
		return "", config.Resolved{}, err
	}
	r, err := resolve(cfg, profiles, opts)
	return strings.Join(profiles, "+"), r, err
}

// ResolveConfig resolves profiles against an already-loaded config through the
// same decryption and degradation seam the read commands use. It exists for a
// caller holding a merged view that must not load a second time — a second load
// would replay the load advisories and re-read the layers mid-command.
func ResolveConfig(cfg config.Config, profiles []string, opts Options) (config.Resolved, error) {
	return resolve(cfg, profiles, opts)
}

// Chdir changes to dir when non-empty. Wired into PersistentPreRunE and the
// completion handlers, which bypass it; "" is a no-op.
func Chdir(dir string) error {
	if dir == "" {
		return nil
	}
	return os.Chdir(dir)
}

// resolve walks the profile's extends chain, transparently decrypts any
// enc:v3: values, and expands $VAR interpolation unless opts.NoExpand. A key
// is required only when encrypted values are present, and then only a missing
// key is forgivable: with opts.TolerateMissingKey the ciphertext stays in place
// and each such key is reported in Undecryptable. A present key that cannot
// read a value a remote supplied degrades the same way under
// opts.TolerateRemoteWrongKey and refuses the profile otherwise; one that cannot
// read a user-layer value is always an error. Comments ride through untouched.
func resolve(cfg config.Config, profiles []string, opts Options) (config.Resolved, error) {
	r, err := cfg.ResolveProfiles(profiles)
	if err != nil {
		return config.Resolved{}, err
	}
	// Record ciphertext before decryption overwrites it: masking and the
	// plaintext-secret warning both key off how a value sits at rest.
	for k, v := range r.Env {
		if crypto.IsEncrypted(v) {
			if r.Encrypted == nil {
				r.Encrypted = map[string]bool{}
			}
			r.Encrypted[k] = true
		}
	}
	if prefix := foreignEncPrefix(r.Env); prefix != "" {
		return config.Resolved{}, crypto.ForeignEncError(prefix)
	}
	if !hasEncrypted(r.Env) {
		if !opts.NoExpand {
			r.Env = varsubst.Expand(r.Env, osEnvMap())
		}
		return r, nil
	}
	key, _, err := ResolveKeyOrPrompt(opts, func() ([]byte, crypto.Argon2Params, string, error) {
		return firstSaltAndSample(r.Env)
	})
	if err != nil {
		if !errors.Is(err, ErrNoKey) {
			return config.Resolved{}, err
		}
		if !opts.TolerateMissingKey {
			return config.Resolved{}, WithEncryptedKeys(err, slices.Collect(maps.Keys(r.Encrypted)))
		}
		// Only an absent key degrades: the ciphertext stays for the renderer to
		// annotate, and the values a key could read still expand.
		r.Undecryptable = make(map[string]string, len(r.Encrypted))
		for k := range r.Encrypted {
			r.Undecryptable[k] = noKeyReason
		}
		if !opts.NoExpand {
			r.Env = expandReadable(r.Env, r.Encrypted)
		}
		return r, nil
	}
	// A value a remote supplied is ciphertext its author may have written for a
	// key nobody here holds, so a failure there costs only that key; a user-layer
	// failure is a key mismatch about the user's own file and stays loud. Every
	// failure is collected before deciding, so a refusal names them all.
	var remoteWrong []string
	for k, v := range r.Env {
		if !crypto.IsEncrypted(v) {
			continue
		}
		plain, err := crypto.DecryptValue(v, key)
		if err != nil {
			if !remoteSourced(r.Sources, k) {
				return config.Resolved{}, fmt.Errorf("decrypt %s: %w", k, err)
			}
			remoteWrong = append(remoteWrong, k)
			continue
		}
		r.Env[k] = plain
	}
	if len(remoteWrong) > 0 {
		slices.Sort(remoteWrong)
		if !opts.TolerateRemoteWrongKey {
			return config.Resolved{}, wrongRemoteKeyError(remoteWrong)
		}
		r.Undecryptable = make(map[string]string, len(remoteWrong))
		unread := make(map[string]bool, len(remoteWrong))
		for _, k := range remoteWrong {
			r.Undecryptable[k] = wrongKeyReason
			unread[k] = true
		}
		if !opts.NoExpand {
			r.Env = expandReadable(r.Env, unread)
		}
		return r, nil
	}
	if !opts.NoExpand {
		r.Env = varsubst.Expand(r.Env, osEnvMap())
	}
	return r, nil
}

// remoteSourced reports whether k's winning entry came from a remote's content,
// the only provenance whose ciphertext a consumer may legitimately lack the key
// for. A value with no recorded source counts as the user's own, so an
// unattributable failure never degrades quietly.
func remoteSourced(sources map[string]config.Source, k string) bool {
	s, ok := envname.Get(sources, k)
	return ok && config.IsRemoteLayer(s.Layer)
}

// wrongRemoteKeyError refuses a path that must not carry a value it cannot
// vouch for — a child environment or a file export — so a secret never goes
// missing quietly: name every key, and the patch path the pull warning already
// documents. Plaintext patches while foreign ciphertext remains, encryption only
// once every foreign value is replaced: two salts in one profile break
// passphrase recovery.
func wrongRemoteKeyError(keys []string) error {
	return fmt.Errorf("remote ciphertext cannot be decrypted with the configured key: %s; patch these keys with plaintext values in your own layer while foreign ciphertext remains, and encrypt only after every foreign value is replaced", strings.Join(keys, ", "))
}

// Run is the full run path: parse args, load config, resolve the profile, and
// exec the child command with the merged environment. The child's non-zero exit
// code is propagated via os.Exit (cobra's RunE only signals 0/1).
func Run(args []string, dashAt int, opts Options) error {
	profiles, cmdArgs := ParseProfilesAndCmd(args, dashAt)
	if len(cmdArgs) == 0 {
		if dashAt < 0 && len(args) > 1 {
			rest := strings.Join(args[1:], " ")
			return fmt.Errorf("%s: no `--` before the command; interpreted %q as the profile and dropped %q; use `%s`", opts.Name, args[0], rest, config.HintCommand(strings.TrimPrefix(opts.Name, "enver "), args[0], "--", rest))
		}
		return fmt.Errorf("%s requires a command after `--`", opts.Name)
	}
	label, r, err := ResolveProfiles(opts, profiles)
	if err != nil {
		return err
	}
	env, notice, err := childEnv(opts, r.Env)
	if err != nil {
		return err
	}
	if notice != "" {
		fmt.Fprintf(os.Stderr, "%s: %s\n", opts.Name, notice)
	}
	if code := runner.Run(cmdArgs, env, opts.Name, label); code != 0 {
		os.Exit(code)
	}
	return nil
}

// profilesOrDefault applies the config default to an empty profile list and
// errors when nothing is selectable. Centralizes the guard shared by Run,
// show, export, and dotenv so the fallback and message stay consistent.
func profilesOrDefault(profiles []string, def string) ([]string, error) {
	if len(profiles) > 0 {
		return profiles, nil
	}
	if def == "" {
		return nil, fmt.Errorf("no profile specified and no default set in config; set one with `%s`", config.HintCommand("default", "<profile>"))
	}
	return []string{def}, nil
}

// ParseProfilesAndCmd splits args at the `--` separator (cobra's ArgsLenAtDash).
// Every token before the dash is a profile, composed left-to-right; everything
// after is the child command and its own args. Without a dash nothing is
// interpretable — there is no command position to anchor the split — so both
// halves come back empty and Run raises the loud usage errors.
func ParseProfilesAndCmd(args []string, dashAt int) (profiles []string, cmdArgs []string) {
	if dashAt >= 0 {
		return args[:dashAt], args[dashAt:]
	}
	return nil, nil
}

// SaltSource locates the salt, KDF parameters, and a full sample value of
// some enc:v3: value, for passphrase recovery when no key is configured. A
// nil salt means the source holds no encrypted value. Called only on the
// no-key path, so expensive sources stay idle when a key resolves.
type SaltSource func() (salt []byte, params crypto.Argon2Params, sample string, err error)

// ResolveKeyOrPrompt resolves the decryption key from --key, the ENVER_KEY env
// var, or the default key file, recovering it from a passphrase via saltSource
// when none is available. salt is nil only for ENVER_KEY keys, which can
// decrypt (the salt is embedded in each enc:v3: value) but not encrypt.
func ResolveKeyOrPrompt(opts Options, saltSource SaltSource) (key, salt []byte, err error) {
	key, salt, err = resolveKey(opts)
	if err != nil || key != nil {
		return key, salt, err
	}
	salt, params, sample, err := saltSource()
	if err != nil {
		return nil, nil, err
	}
	if salt == nil {
		return nil, nil, ErrNoKey
	}
	key, err = recoverKey(salt, params, sample)
	if err != nil {
		return nil, nil, err
	}
	return key, salt, nil
}

// KeyAvailable reports whether a key resolves. It never prompts — resolveKey
// reads --key, ENVER_KEY, and the key cache only — so a caller can consult it
// before a write that would need to decrypt. A key source that fails to load is
// unavailable, not available: only a resolved key counts.
func KeyAvailable(opts Options) bool {
	key, _, err := resolveKey(opts)
	return key != nil && err == nil
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

// recoverKey prompts for a passphrase, derives the key from salt with the
// value-embedded KDF parameters, verifies it by decrypting sample, and writes
// the key cache. Returns the derived key.
func recoverKey(salt []byte, p crypto.Argon2Params, sample string) ([]byte, error) {
	if !Interactive() {
		return nil, ErrNoKey
	}
	if PromptPassphrase == nil {
		return nil, fmt.Errorf("passphrase recovery is not configured")
	}
	pass, err := PromptPassphrase("Enter passphrase:")
	if err != nil {
		return nil, err
	}
	key, err := crypto.DeriveKey(pass, salt, p)
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
// (cobra ShellCompDirective), keeping this package free of cobra. Completion is
// a silent read: the candidates are the whole output, so a load warning would be
// injected into the shell's own stream.
func MatchingProfiles(opts Options, toComplete string) []string {
	return matchingProfiles(opts, toComplete, nil)
}

// MatchingRemoteProfiles returns the merged profile names beginning with
// toComplete that remote content projects, for the completion of commands that
// can bootstrap a patch copy over such a name. It never returns a user layer's
// own name: those commands still refuse a name only another layer owns. The
// load stays silent, exactly as MatchingProfiles does.
func MatchingRemoteProfiles(opts Options, toComplete string) []string {
	return matchingProfiles(opts, toComplete, func(cfg config.Config, name string) bool {
		return config.IsRemoteLayer(cfg.ProfileLayer[name])
	})
}

// matchingProfiles is the one candidate scan both completion variants share;
// keep decides which merged names are eligible, and nil admits every one.
func matchingProfiles(opts Options, toComplete string, keep func(config.Config, string) bool) []string {
	cfg, err := loadMerged(opts, false)
	if err != nil {
		return nil
	}
	names := cfg.ProfileNames()
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !strings.HasPrefix(n, toComplete) {
			continue
		}
		if keep != nil && !keep(cfg, n) {
			continue
		}
		out = append(out, n)
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

func foreignEncPrefix(env map[string]string) string {
	for _, v := range env {
		if p := crypto.ForeignEncPrefix(v); p != "" {
			return p
		}
	}
	return ""
}

// expandReadable expands the values a key could read and restores the
// unreadable ciphertext exactly as it sits at rest. Each unreadable key is
// seeded empty so a plaintext value depending on it resolves to nothing instead
// of borrowing the process environment's same-named variable: the profile's own
// entry wins, unreadable or not.
func expandReadable(env map[string]string, unreadable map[string]bool) map[string]string {
	seed := make(map[string]string, len(env))
	for k, v := range env {
		if unreadable[k] {
			v = ""
		}
		seed[k] = v
	}
	out := varsubst.Expand(seed, osEnvMap())
	for k := range unreadable {
		out[k] = env[k]
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
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

// childEnv merges the process environment with the resolved profile env for
// the exec path, dropping ENVER_KEY first: enver's own decryption credential
// must not ride along into every child process, unlike the user's shell
// exports. Interpolation still sees it — a profile may reference it
// explicitly. ENVER_FORWARD_KEY (any non-empty value) opts out so nested enver
// invocations can decrypt. The returned notice, when non-empty, is printed to
// stderr before exec: dropping a key the user set is the one silent behavior
// change worth announcing.
func childEnv(opts Options, profileEnv map[string]string) (env []string, notice string, err error) {
	m := osEnvMap()
	if os.Getenv("ENVER_FORWARD_KEY") != "" {
		key, _, err := resolveKey(opts)
		if err != nil {
			return nil, "", err
		}

		// Re-export the key that actually decrypts, whatever source it came
		// from (--key file, ENVER_KEY, or the key cache), so nested enver
		// invocations under the child (make → enver x) can decrypt too.
		// Salt-less, so the child can decrypt but not encrypt. Without a key
		// there is nothing to forward; a stray empty entry goes too.
		envname.Delete(m, "ENVER_KEY")
		if key != nil {
			envname.Set(m, "ENVER_KEY", crypto.EncodeKey(key))
		}
		return runner.MergedEnv(m, profileEnv), "", nil
	}
	if os.Getenv("ENVER_KEY") != "" {
		notice = "ENVER_KEY is not passed to the child; set ENVER_FORWARD_KEY=1 to forward it"
	}
	envname.Delete(m, "ENVER_KEY")
	return runner.MergedEnv(m, profileEnv), notice, nil
}

// firstSaltAndSample returns the salt, KDF parameters, and full value of an
// enc:v3: value the map iteration reaches in env, or
// (nil, crypto.Argon2Params{}, "", nil) when there is none. Values
// disagreeing on salt or KDF parameters are an error: one passphrase cannot
// recover two eras, and picking one by map order would make recovery flip
// between working and failing per run.
func firstSaltAndSample(env map[string]string) ([]byte, crypto.Argon2Params, string, error) {
	var scan crypto.SaltScan
	for _, v := range env {
		if err := scan.Add(v); err != nil {
			return nil, crypto.Argon2Params{}, "", err
		}
	}
	salt, params, sample := scan.Result()
	return salt, params, sample, nil
}
