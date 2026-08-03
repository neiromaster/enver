package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
)

func doList() error {
	cfg, err := config.LoadMerged(rootFlags.configPath, !rootFlags.noLocal)
	if err != nil {
		return err
	}
	names := cfg.ProfileNames()
	if len(names) == 0 {
		fmt.Println("(no profiles defined)")
		fmt.Printf("\nCreate one at: %s\n", config.GlobalPath(rootFlags.configPath))
		return nil
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
	return nil
}

func doPrint(cfg config.Config, profile string, exportFmt, unmasked bool) error {
	env, chain, err := resolveAndDecrypt(cfg, profile)
	if err != nil {
		return err
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
			v = config.MaskValue(k, v)
		}
		fmt.Printf("%s=%s\n", k, v)
	}
	return nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// resolveAndDecrypt resolves a profile and transparently decrypts any encrypted
// values. A key is only required when encrypted values are present.
func resolveAndDecrypt(cfg config.Config, profile string) (map[string]string, []string, error) {
	env, chain, err := cfg.ResolveProfile(profile)
	if err != nil {
		return nil, nil, err
	}
	if !hasEncrypted(env) {
		return env, chain, nil
	}
	key, err := resolveKey()
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
	return env, chain, nil
}

func resolveKey() ([]byte, error) {
	if rootFlags.keyPath != "" {
		return crypto.LoadKey(rootFlags.keyPath)
	}
	if v := os.Getenv("ENVER_KEY"); v != "" {
		return crypto.DecodeKey(v)
	}
	if path := crypto.KeyFilePath(); fileExists(path) {
		return crypto.LoadKey(path)
	}
	return nil, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func hasEncrypted(env map[string]string) bool {
	for _, v := range env {
		if crypto.IsEncrypted(v) {
			return true
		}
	}
	return false
}