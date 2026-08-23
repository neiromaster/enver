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

var keygenCmd = &cobra.Command{
	Use:   "keygen",
	Short: "Generate the encryption key from a passphrase",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		path := keygenPath()
		if keygenRandom {
			risk, err := keygenRisk(force, path, nil, scanEncrypted)
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
		if !ui.Interactive() {
			return fmt.Errorf("keygen requires a terminal; use --random for a non-interactive key")
		}
		if !force {
			if _, err := os.Stat(path); err == nil {
				return fmt.Errorf("key already exists at %s (use --force to overwrite)", path)
			}
		}
		pass, err := ui.Password("Enter passphrase:")
		if err != nil {
			return err
		}
		confirm, err := ui.Password("Confirm passphrase:")
		if err != nil {
			return err
		}
		if pass != confirm {
			return fmt.Errorf("passphrases do not match")
		}
		scan, err := scanConfigCrypt()
		if err != nil {
			return err
		}
		salt := scan.salt
		if salt == nil {
			salt = make([]byte, crypto.SaltSize)
			if _, err := rand.Read(salt); err != nil {
				return err
			}
		}
		key, err := crypto.DeriveKey(pass, salt)
		if err != nil {
			return err
		}
		risk, err := keygenRisk(force, path, key, func() (bool, error) { return scan.hasEncrypted, nil })
		if err != nil {
			return err
		}
		if risk {
			ok, cerr := ui.Confirm(
				"This passphrase derives a key different from the current one. Overwriting strands existing encrypted values (run `enver decrypt` with the old key first). Overwrite anyway?",
				false)
			if cerr != nil {
				return cerr
			}
			if !ok {
				return fmt.Errorf("aborted: key not overwritten")
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

// scanEncrypted evaluates whether either config layer holds an encrypted value.
func scanEncrypted() (bool, error) {
	scan, err := scanConfigCrypt()
	if err != nil {
		return false, err
	}
	return scan.hasEncrypted, nil
}

// keygenRisk reports whether force-overwriting the key at path with newKey
// would strand encrypted values: an existing key that differs plus encrypted
// values in the configs. newKey is nil when the key is not yet known (--random),
// in which case any existing key counts as different. hasEncrypted is evaluated
// only when the check reaches the configs, so a first-time keygen never reads
// them. A corrupt key file is an error: overwriting it cannot be judged safe.
func keygenRisk(force bool, path string, newKey []byte, hasEncrypted func() (bool, error)) (bool, error) {
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
	return hasEncrypted()
}

// configCryptScan is the single-pass result of scanning both config layers:
// whether any encrypted value exists and the first enc:v2 salt.
type configCryptScan struct {
	hasEncrypted bool
	salt         []byte
}

// scanConfigCrypt parses each config layer once, reporting whether any value is
// encrypted (a new key would strand it) and the first enc:v2 salt (reused so a
// re-run with the same passphrase derives the same key). A corrupt layer is an
// error rather than a silent skip: keygen must not overwrite a key while it
// cannot tell what the configs hold.
func scanConfigCrypt() (configCryptScan, error) {
	var scan configCryptScan
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
				if scan.salt == nil {
					if s, err := crypto.SaltFromValue(v); err == nil {
						scan.salt = s
					}
				}
			}
		}
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

func requireKey() (key, salt []byte, err error) {
	key, salt, err = app.ResolveKey(appOpts())
	if err != nil {
		return nil, nil, err
	}
	if key == nil {
		v, err := config.FirstEncryptedValue(writeTarget())
		if err != nil {
			return nil, nil, err
		}
		if v == "" {
			return nil, nil, fmt.Errorf("no key found; run `enver keygen` or set --key/ENVER_KEY")
		}
		s, err := crypto.SaltFromValue(v)
		if err != nil {
			return nil, nil, fmt.Errorf("no key found; run `enver keygen` or set --key/ENVER_KEY")
		}
		key, err = app.RecoverKey(s, v)
		if err != nil {
			return nil, nil, err
		}
		salt = s
	}
	return key, salt, nil
}

func init() {
	keygenCmd.Flags().BoolP("force", "f", false, "overwrite an existing key")
	keygenCmd.Flags().BoolVar(&keygenRandom, "random", false, "generate a random key instead of deriving from a passphrase")
	encryptCmd.Flags().Bool("all", false, "encrypt every value, not just secret-looking keys")
}
