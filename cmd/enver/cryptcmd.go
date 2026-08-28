package main

import (
	"errors"
	"fmt"

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
		err := app.Keygen(app.KeygenOptions{
			Path:    keygenPath(),
			Random:  keygenRandom,
			Force:   force,
			Scan:    scanCryptForApp,
			Confirm: uiConfirm,
		})
		if errors.Is(err, app.ErrAborted) {
			return aborted(cmd.OutOrStdout())
		}
		if err != nil {
			return err
		}
		fmt.Printf("✓ key cache written to %s (mode 0600)\n", keygenPath())
		if keygenRandom {
			fmt.Println("Keep this file private. Commit encrypted configs, never the key.")
		} else {
			fmt.Println("Recovery: on a new machine, run `enver x <profile> -- <command>` and enter your passphrase when prompted.")
		}
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

// scanCryptForApp adapts the two-layer salt scan to the app.Keygen seam.
// A scan error already names the configs; the wrap here would only repeat it.
func scanCryptForApp() (app.CryptScan, error) {
	var salts crypto.SaltScan
	for _, p := range []string{config.GlobalPath(globalFlags.configPath), config.LocalPath()} {
		if err := config.ScanCrypt(p, &salts); err != nil {
			return app.CryptScan{}, err
		}
	}
	salt, params, sample := salts.Result()
	return app.CryptScan{Salt: salt, Params: params, Sample: sample}, nil
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
