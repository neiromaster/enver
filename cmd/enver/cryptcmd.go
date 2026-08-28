package main

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"os"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

var keygenRandom bool

var uiConfirm = ui.Confirm

var keygenCmd = &cobra.Command{
	Use:   "keygen",
	Short: "Generate the encryption key from a passphrase",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		path := keygenPath()
		if keygenRandom {
			risk, err := keygenRisk(force, path, nil, func() (bool, error) {
				scan, err := scanConfigCrypt()
				if err != nil {
					return false, err
				}
				return scan.hasEncrypted, nil
			})
			if err != nil {
				return err
			}
			if risk {
				fmt.Fprintln(os.Stderr, "warning: overwriting the key makes existing encrypted values unreadable; run `enver decrypt` with the old key first to migrate")
			}
			if err := crypto.GenerateKey(path, force); err != nil {
				return err
			}
			fmt.Printf("✓ key cache written to %s (mode 0600)\n", path)
			fmt.Println("Keep this file private. Commit encrypted configs, never the key.")
			return nil
		}
		if !app.Interactive() {
			return fmt.Errorf("keygen requires a terminal; use --random for a non-interactive key")
		}
		if !force {
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("key already exists at %s (use --force to overwrite)", path)
			}
		}
		scan, err := scanConfigCrypt()
		if err != nil {
			return err
		}
		pass, err := app.PromptPassphrase("Enter passphrase:")
		if err != nil {
			return err
		}
		confirm, err := app.PromptPassphrase("Confirm passphrase:")
		if err != nil {
			return err
		}
		if pass != confirm {
			return fmt.Errorf("passphrases do not match")
		}
		salt := scan.salt
		params := scan.params
		if salt == nil {
			salt = make([]byte, crypto.SaltSize)
			if _, err := rand.Read(salt); err != nil {
				return err
			}
			params = crypto.CurrentParams
		}
		key, err := crypto.DeriveKey(pass, salt, params)
		if err != nil {
			return err
		}
		// The key reuses the configs' salt, so a passphrase that cannot decrypt
		// the sample is not the one the existing values were encrypted with.
		// Verified before the risk gate: a passphrase that decrypts the sample
		// strands nothing, so it is never warned about as stranding — it is the
		// recovery path for a lost or rotated key cache. Every non-force path
		// must refuse rather than cache a key no value in the configs opens with.
		matches := false
		if scan.hasEncrypted {
			_, derr := crypto.DecryptValue(scan.sample, key)
			matches = derr == nil
		}
		risk, err := keygenRisk(force, path, key, func() (bool, error) { return scan.hasEncrypted && !matches, nil })
		if err != nil {
			return err
		}
		if scan.hasEncrypted && !matches && !risk {
			return fmt.Errorf("passphrase does not match the encrypted values in the configs; they stay tied to the passphrase that encrypted them — use it, or remove those values first")
		}
		if risk {
			ok, cerr := uiConfirm(
				"This passphrase derives a key different from the current one. Overwriting strands existing encrypted values (recover them first with the key that encrypted them). Overwrite anyway?",
				false)
			if cerr != nil {
				return cerr
			}
			if !ok {
				return aborted(cmd.OutOrStdout())
			}
		}
		if err := crypto.WriteKeyCache(path, crypto.NewKeyCache(salt, key)); err != nil {
			return err
		}
		fmt.Printf("✓ key cache written to %s (mode 0600)\n", path)
		fmt.Println("Recovery: on a new machine, run `enver x <profile> -- <command>` and enter your passphrase when prompted.")
		return nil
	},
}

// keygenPath is the file keygen writes: --key when given, else the default.
// The guard and the write share it so a forced overwrite judges the same key it
// replaces.
func keygenPath() string {
	if globalFlags.keyPath != "" {
		return globalFlags.keyPath
	}
	return crypto.KeyFilePath()
}

// keygenRisk reports whether force-overwriting the key at path with newKey
// would strand encrypted values: an existing key that differs plus values the
// replacement cannot read. newKey is nil when the key is not yet known
// (--random), in which case any existing key counts as different and any
// encrypted value counts as stranded. wouldStrand is called only when the
// overwrite is real, so paths that can strand nothing (--random or
// interactive keygen without --force) never read the configs: --random is the
// bootstrap path and must work beside unreadable or foreign-enc configs. A
// corrupt key file is an error: overwriting it cannot be judged safe.
func keygenRisk(force bool, path string, newKey []byte, wouldStrand func() (bool, error)) (bool, error) {
	if !force {
		return false, nil
	}
	old, _, err := crypto.LoadKey(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil // no existing key file: nothing to strand
		}
		return false, fmt.Errorf("existing key at %s is unreadable: %w; fix or remove it before forcing a new key", path, err)
	}
	if len(old) == 0 {
		return false, nil
	}
	if newKey != nil && bytes.Equal(old, newKey) {
		return false, nil // same key: safe to rewrite
	}
	return wouldStrand()
}

// configCryptScan is the single-pass result of scanning both config layers:
// whether any encrypted value exists and the first enc:v3 salt, KDF params,
// and sample value.
type configCryptScan struct {
	hasEncrypted bool
	salt         []byte
	params       crypto.Argon2Params
	sample       string
}

// scanConfigCrypt parses each config layer once, reporting whether any value
// is encrypted (a new key would strand it) and the first enc:v3 salt, KDF
// params, and sample value (the salt and params are reused so a re-run with
// the same passphrase derives the same key; the sample verifies the
// passphrase against the values it must decrypt). Values from different eras
// — disagreeing salt or KDF params — are an error: one passphrase cannot
// recover both. A corrupt layer is an error rather than a silent skip: keygen
// must not overwrite a key while it cannot tell what the configs hold.
func scanConfigCrypt() (configCryptScan, error) {
	var scan configCryptScan
	var salts crypto.SaltScan
	for _, p := range []string{config.GlobalPath(globalFlags.configPath), config.LocalPath()} {
		c, err := config.LoadFile(p)
		if err != nil {
			return scan, fmt.Errorf("cannot scan configs for encrypted values: %w", err)
		}
		for _, prof := range c.Profiles {
			for _, v := range prof.Env {
				if crypto.IsEncrypted(v) {
					scan.hasEncrypted = true
				}
				if err := salts.Add(v); err != nil {
					return scan, err
				}
			}
		}
	}
	if salts.Found() {
		scan.salt, scan.params, scan.sample = salts.Result()
	}
	return scan, nil
}

var encryptCmd = &cobra.Command{
	Use:               "encrypt [profile]",
	Short:             "Encrypt secret values in the config",
	Args:              cobra.MaximumNArgs(1),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfileInTarget,
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		profile := ""
		if len(args) > 0 {
			profile = args[0]
		}
		key, salt, err := requireKey()
		if err != nil {
			return err
		}
		if salt == nil {
			return fmt.Errorf("ENVER_KEY carries no salt, so it cannot encrypt; run `enver keygen` or pass --key <key cache>")
		}
		path := writeTarget()
		n, err := config.EncryptFile(path, key, salt, profile, all)
		if err != nil {
			return err
		}
		fmt.Printf("✓ encrypted %d value(s) in %s\n", n, path)
		return nil
	},
}

var decryptCmd = &cobra.Command{
	Use:               "decrypt [profile]",
	Short:             "Decrypt values back to plaintext",
	Args:              cobra.MaximumNArgs(1),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfileInTarget,
	RunE: func(cmd *cobra.Command, args []string) error {
		profile := ""
		if len(args) > 0 {
			profile = args[0]
		}
		key, _, err := requireKey()
		if err != nil {
			return err
		}
		path := writeTarget()
		n, err := config.DecryptFile(path, key, profile)
		if err != nil {
			return err
		}
		fmt.Printf("✓ decrypted %d value(s) in %s\n", n, path)
		return nil
	},
}

// requireKey resolves the key for encrypt/decrypt, recovering it from a
// passphrase when no key is configured. The recovery salt and KDF parameters
// come from the write target, the same file the commands rewrite.
func requireKey() ([]byte, []byte, error) {
	return app.ResolveKeyOrPrompt(appOpts(), func() ([]byte, crypto.Argon2Params, string, error) {
		return config.FirstSaltAndSample(writeTarget())
	})
}

func init() {
	keygenCmd.Flags().BoolP("force", "f", false, "overwrite an existing key")
	keygenCmd.Flags().BoolVar(&keygenRandom, "random", false, "generate a random key instead of deriving from a passphrase")
	encryptCmd.Flags().Bool("all", false, "encrypt every value, not just secret-looking keys")
}
