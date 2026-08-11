package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func validateProfileName(name string) error {
	if !profileNameRe.MatchString(name) {
		return fmt.Errorf("invalid profile name %q", name)
	}
	return nil
}

// promptProfileName reads a profile name interactively, re-prompting on invalid
// input. It returns the trimmed name and ok=true, or ok=false when the input
// prompt fails (for example EOF), in which case the caller aborts silently.
func promptProfileName() (string, bool) {
	for {
		n, err := ui.Input("Profile name")
		if err != nil {
			return "", false
		}
		n = strings.TrimSpace(n)
		if err := validateProfileName(n); err != nil {
			fmt.Println("  invalid: use letters, digits, '-' or '_'; must start with a letter or digit")
			continue
		}
		return n, true
	}
}

func buildProfile(extends string, entries []ui.EnvEntry) (config.Profile, map[string]string) {
	env := make(map[string]string, len(entries))
	comments := map[string]string{}
	for _, e := range entries {
		env[e.Key] = e.Value
		if e.Comment != "" {
			comments[e.Key] = e.Comment
		}
	}
	return config.Profile{Extends: config.Extends{extends}, Env: env}, comments
}

// buildSummary builds the display summary for the collecting env-card: own entries
// (marked Override when they shadow an inherited key) in insertion order, then the
// inherited keys not defined as own (sorted). Values are masked via config.MaskValue.
// entries is not mutated.
func buildSummary(entries []ui.EnvEntry, parentEnv map[string]string) []ui.SummaryEntry {
	out := make([]ui.SummaryEntry, 0, len(entries)+len(parentEnv))
	own := make(map[string]bool, len(entries))
	for _, e := range entries {
		own[e.Key] = true
		kind := ui.EntryAdded
		if _, ok := parentEnv[e.Key]; ok {
			kind = ui.EntryOverride
		}
		out = append(out, ui.SummaryEntry{Key: e.Key, Value: config.MaskValue(e.Key, e.Value), Kind: kind})
	}
	inherited := make([]string, 0, len(parentEnv))
	for k := range parentEnv {
		if !own[k] {
			inherited = append(inherited, k)
		}
	}
	sort.Strings(inherited)
	for _, k := range inherited {
		out = append(out, ui.SummaryEntry{Key: k, Value: config.MaskValue(k, parentEnv[k]), Kind: ui.EntryInherited})
	}
	return out
}

// upsertEntry appends entry, or replaces the existing entry with the same key.
func upsertEntry(entries []ui.EnvEntry, e ui.EnvEntry) []ui.EnvEntry {
	for i := range entries {
		if entries[i].Key == e.Key {
			entries[i] = e
			return entries
		}
	}
	return append(entries, e)
}

var addCmd = &cobra.Command{
	Use:   "add [name]",
	Short: "Interactively add a profile",
	Args:  cobra.MaximumNArgs(1),
	RunE:  doAdd,
}

func doAdd(cmd *cobra.Command, args []string) error {
	if err := interactiveOnly("add"); err != nil {
		return err
	}
	cfgPath := writeTarget()
	targetCfg, _ := config.LoadFile(cfgPath)
	pickerCfg, err := pickerConfig()
	if err != nil {
		return err
	}
	names := pickerCfg.ProfileNames()

	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		n, ok := promptProfileName()
		if !ok {
			return nil
		}
		name = n
	} else if err := validateProfileName(name); err != nil {
		return err
	}

	extends := ""
	if len(names) > 0 {
		opts := []ui.Option{{Value: "", Label: "(none)"}}
		for _, n := range names {
			opts = append(opts, ui.Option{Value: n, Label: n})
		}
		defaultExtends := ""
		if e := targetCfg.Profiles[name].Extends; len(e) > 0 {
			defaultExtends = e[0]
		}
		picked, err := ui.SelectDefault("Extends", opts, defaultExtends)
		if err != nil {
			return nil
		}
		extends = picked
	}

	parentEnv := map[string]string{}
	if extends != "" {
		if resolved, _, err := pickerCfg.ResolveProfile(extends); err == nil {
			parentEnv = resolved
		}
	}

	var entries []ui.EnvEntry
	for {
		entry, err := ui.EnvCardCollecting(ui.EnvEntry{}, buildSummary(entries, parentEnv))
		if err != nil {
			return nil
		}
		entry.Key = strings.TrimSpace(entry.Key)
		if entry.Key == "" {
			break
		}
		if strings.ContainsAny(entry.Key, " \t") {
			fmt.Println("  skip: invalid key (no spaces)")
			continue
		}
		entries = upsertEntry(entries, entry)
	}
	if len(entries) == 0 && extends == "" {
		return fmt.Errorf("a profile needs at least one env var or an extends")
	}

	setDefault := false
	if targetCfg.Default == "" {
		ans, err := ui.Confirm(fmt.Sprintf("Set %q as the default profile?", name), true)
		if err != nil {
			return nil
		}
		setDefault = ans
	} else {
		ans, err := ui.Confirm(fmt.Sprintf("Set %q as the default? (current: %s)", name, targetCfg.Default), false)
		if err != nil {
			return nil
		}
		setDefault = ans
	}

	profile, comments := buildProfile(extends, entries)
	if err := config.UpsertProfile(cfgPath, name, profile, setDefault, true, comments); err != nil {
		return err
	}
	fmt.Printf("\n✓ wrote profile %q to %s\n", name, cfgPath)
	if setDefault {
		fmt.Printf("✓ set as default\n")
	}
	fmt.Printf("\nUse it: enver x %s -- <command>\n", name)
	return nil
}
