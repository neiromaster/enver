package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
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

// parentEnvFor resolves the picked extends chain as the inherited backdrop for
// the collecting summary: parents merge left-to-right, while the target
// profile's own env and working fences are stripped, so its own keys never
// read as inherited and a declared unset cannot hide a parent key from the
// summary. When the chain does not resolve, each parent's subtree resolves on
// its own — one broken ancestor must not blank every healthy parent — and the
// healthy ones still merge in pick order, while the broken ones come back as
// warnings for the caller to print.
func parentEnvFor(cfg config.Config, self string, extends config.Extends) (map[string]string, []string) {
	if len(extends) == 0 {
		return map[string]string{}, nil
	}
	blankSelf := func(p *config.Profile) {
		p.Env = nil
		*p = stripWorkingFences(*p)
	}
	if r, err := probeWith(cfg, self, func(p *config.Profile) {
		p.Extends = extends
		blankSelf(p)
	}).ResolveProfile(self); err == nil {
		return r.Env, nil
	}
	backdrop := map[string]string{}
	var warns []string
	for _, parent := range extends {
		br, berr := probeWith(cfg, self, func(p *config.Profile) {
			p.Extends = config.Extends{parent}
			blankSelf(p)
		}).ResolveProfile(self)
		if berr != nil {
			warns = append(warns, fmt.Sprintf("note: parent %q is unresolvable (%v) — its inherited keys are hidden", parent, berr))
			continue
		}
		maps.Copy(backdrop, br.Env)
	}
	return backdrop, warns
}

func buildProfile(extends config.Extends, entries []ui.EnvEntry) (config.Profile, map[string]string) {
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
	slices.Sort(inherited)
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
	targetCfg, err := config.LoadFile(cfgPath)
	if err != nil {
		return err
	}
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

	var extends config.Extends
	if len(names) > 0 {
		seed := targetCfg.Profiles[name].Extends
		opts := seedOptions(seed, profileOptions(pickerCfg, name))
		picked, confirmed, err := ui.MultiSelectOrdered("Extends", opts, seed)
		if err != nil || !confirmed {
			return nil
		}
		extends = picked
	}
	// Resolution spans both layers, so every extends judgment uses the merged
	// view: a global pick can loop through a local parent and vice versa, and
	// the inherited backdrop must resolve through the same parents the cycle
	// check just cleared.
	merged := pickerCfg
	if globalFlags.global {
		if merged, err = app.Load(appOpts()); err != nil {
			return err
		}
	}
	if len(extends) > 0 && extendsCycles(merged, name, extends) {
		return fmt.Errorf("extends %s would create a cycle", strings.Join(extends, ", "))
	}

	parentEnv, inheritedWarns := parentEnvFor(merged, name, extends)
	for _, w := range inheritedWarns {
		fmt.Println("  " + w)
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
		entries = upsertEntry(entries, entry)
	}
	if len(entries) == 0 && len(extends) == 0 {
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
	profile.Comments = comments
	if err := config.UpsertProfile(cfgPath, name, profile, setDefault, true); err != nil {
		return err
	}
	fmt.Printf("\n✓ wrote profile %q to %s\n", name, cfgPath)
	if setDefault {
		fmt.Printf("✓ set as default\n")
	}
	fmt.Printf("\nUse it: enver x %s -- <command>\n", name)
	return nil
}
