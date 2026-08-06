package main

import (
	"fmt"
	"regexp"
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
		entry, err := ui.EnvCard(ui.EnvEntry{})
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
