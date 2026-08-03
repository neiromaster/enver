package main

import (
	"fmt"
	"os"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/spf13/cobra"
)

var keygenCmd = &cobra.Command{
	Use:   "keygen",
	Short: "Generate the encryption key file",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		force, _ := cmd.Flags().GetBool("force")
		path := crypto.KeyFilePath()
		if err := crypto.GenerateKey(path, force); err != nil {
			return err
		}
		fmt.Printf("✓ key written to %s (mode 0600)\n", path)
		fmt.Println("Keep this file private. Commit encrypted configs, never the key.")
		return nil
	},
}

var encryptCmd = &cobra.Command{
	Use:   "encrypt [profile]",
	Short: "Encrypt secret values in the config",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		profile := ""
		if len(args) > 0 {
			profile = args[0]
		}
		key, err := requireKey()
		if err != nil {
			fmt.Fprintln(os.Stderr, "run `enver keygen` first")
			return err
		}
		path := config.GlobalPath(rootFlags.configPath)
		n, err := config.EncryptFile(path, key, profile, all)
		if err != nil {
			return err
		}
		fmt.Printf("✓ encrypted %d value(s) in %s\n", n, path)
		return nil
	},
}

var decryptCmd = &cobra.Command{
	Use:   "decrypt [profile]",
	Short: "Decrypt values back to plaintext",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profile := ""
		if len(args) > 0 {
			profile = args[0]
		}
		key, err := requireKey()
		if err != nil {
			return err
		}
		path := config.GlobalPath(rootFlags.configPath)
		n, err := config.DecryptFile(path, key, profile)
		if err != nil {
			return err
		}
		fmt.Printf("✓ decrypted %d value(s) in %s\n", n, path)
		return nil
	},
}

func requireKey() ([]byte, error) {
	if rootFlags.keyPath != "" {
		return crypto.LoadKey(rootFlags.keyPath)
	}
	if v := os.Getenv("ENVER_KEY"); v != "" {
		return crypto.DecodeKey(v)
	}
	path := crypto.KeyFilePath()
	if !fileExists(path) {
		return nil, fmt.Errorf("no key found at %s", path)
	}
	return crypto.LoadKey(path)
}

func init() {
	keygenCmd.Flags().BoolP("force", "f", false, "overwrite an existing key")
	encryptCmd.Flags().Bool("all", false, "encrypt every value, not just secret-looking keys")
}