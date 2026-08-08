package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/neiromaster/enver/internal/app"
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

func buildProfile(extends string, entries []ui.EnvEntry) (config.Profile, map[string]string) {
	env := make(map[string]string, len(entries))
	comments := map[string]string{}
	for _, e := range entries {
		env[e.Key] = e.Value
		if e.Comment != "" {
			comments[e.Key] = e.Comment
		}
	}
	return config.Profile{Extends: extends, Env: env}, comments
}

// maskedEntries returns a display-only copy of entries with secret values
// redacted via config.MaskValue. The input slice is not mutated; keys and
// comments pass through unchanged.
func maskedEntries(entries []ui.EnvEntry) []ui.EnvEntry {
	out := make([]ui.EnvEntry, len(entries))
	for i, e := range entries {
		out[i] = ui.EnvEntry{Key: e.Key, Value: config.MaskValue(e.Key, e.Value), Comment: e.Comment}
	}
	return out
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
	cfgPath := config.GlobalPath(globalFlags.configPath)
	existing, _ := app.Load(app.Options{ConfigPath: globalFlags.configPath, NoLocal: true})
	names := existing.ProfileNames()

	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		for {
			n, err := ui.Input("Profile name")
			if err != nil {
				return nil
			}
			n = strings.TrimSpace(n)
			if err := validateProfileName(n); err != nil {
				fmt.Println("  invalid: use letters, digits, '-' or '_'; must start with a letter or digit")
				continue
			}
			name = n
			break
		}
	} else if err := validateProfileName(name); err != nil {
		return err
	}

	extends := ""
	if len(names) > 0 {
		opts := []ui.Option{{Value: "", Label: "(none)"}}
		for _, n := range names {
			opts = append(opts, ui.Option{Value: n, Label: n})
		}
		picked, err := ui.Select("Extends", opts)
		if err != nil {
			return nil
		}
		extends = picked
	}

	var entries []ui.EnvEntry
	for {
		entry, err := ui.EnvCardCollecting(ui.EnvEntry{}, maskedEntries(entries))
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
		entries = append(entries, entry)
	}
	if len(entries) == 0 && extends == "" {
		return fmt.Errorf("a profile needs at least one env var or an extends")
	}

	setDefault := false
	if existing.Default == "" {
		ans, err := ui.Confirm(fmt.Sprintf("Set %q as the default profile?", name), true)
		if err != nil {
			return nil
		}
		setDefault = ans
	} else {
		ans, err := ui.Confirm(fmt.Sprintf("Set %q as the default? (current: %s)", name, existing.Default), false)
		if err != nil {
			return nil
		}
		setDefault = ans
	}

	profile, comments := buildProfile(extends, entries)
	if err := config.UpsertProfile(cfgPath, name, profile, setDefault, comments); err != nil {
		return err
	}
	fmt.Printf("✓ wrote profile %q to %s\n", name, cfgPath)
	if setDefault {
		fmt.Printf("✓ set as default\n")
	}
	fmt.Printf("\nUse it: enver x %s -- <command>\n", name)
	return nil
}
