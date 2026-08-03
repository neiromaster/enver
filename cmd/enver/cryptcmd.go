package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
)

func doCryptCmd(name string, args []string) int {
	switch name {
	case "keygen":
		return doKeygen(args)
	case "encrypt":
		return doEncrypt(args)
	case "decrypt":
		return doDecrypt(args)
	}
	return 2
}

func doKeygen(args []string) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	var force bool
	fs.BoolVar(&force, "force", false, "overwrite an existing key")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path := crypto.KeyFilePath()
	if err := crypto.GenerateKey(path, force); err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		return 1
	}
	fmt.Printf("✓ key written to %s (mode 0600)\n", path)
	fmt.Println("Keep this file private. Commit encrypted configs, never the key.")
	return 0
}

func cryptFlags(name string, args []string) (profile string, all bool, cfgPath, keyPath string, code int) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.StringVar(&profile, "profile", "", "operate on a single profile (default: all)")
	fs.BoolVar(&all, "all", false, "encrypt every value, not just secret-looking keys (encrypt only)")
	fs.StringVar(&cfgPath, "config", "", "config file path")
	fs.StringVar(&keyPath, "key", "", "key file path (default: ENVER_KEY env or ~/.config/enver/key)")
	if err := fs.Parse(args); err != nil {
		return "", false, "", "", 2
	}
	rest := fs.Args()
	if len(rest) > 0 {
		profile = rest[0]
	}
	return profile, all, cfgPath, keyPath, 0
}

func doEncrypt(args []string) int {
	profile, all, cfgPath, keyPath, code := cryptFlags("encrypt", args)
	if code != 0 {
		return code
	}
	key, err := cryptKey(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		fmt.Fprintln(os.Stderr, "run `enver keygen` first")
		return 1
	}
	path := config.GlobalPath(cfgPath)
	n, err := config.EncryptFile(path, key, profile, all)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		return 1
	}
	fmt.Printf("✓ encrypted %d value(s) in %s\n", n, path)
	return 0
}

func doDecrypt(args []string) int {
	profile, _, cfgPath, keyPath, code := cryptFlags("decrypt", args)
	if code != 0 {
		return code
	}
	key, err := cryptKey(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		return 1
	}
	path := config.GlobalPath(cfgPath)
	n, err := config.DecryptFile(path, key, profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		return 1
	}
	fmt.Printf("✓ decrypted %d value(s) in %s\n", n, path)
	return 0
}

func cryptKey(keyPath string) ([]byte, error) {
	if keyPath != "" {
		return crypto.LoadKey(keyPath)
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