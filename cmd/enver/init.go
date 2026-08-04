package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/neiromaster/enver/internal/config"
	"github.com/spf13/cobra"
)

var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// reservedSubcommands shadow profiles in the bare form — reach them via `enver run`.
var reservedSubcommands = []string{"run", "init", "keygen", "encrypt", "decrypt", "completion", "help"}

var initCmd = &cobra.Command{
	Use:   "init [name]",
	Short: "Interactively create a profile",
	Args:  cobra.MaximumNArgs(1),
	RunE:  doInit,
}

func doInit(cmd *cobra.Command, args []string) error {
	cfgPath := config.GlobalPath(rootFlags.configPath)
	reader := bufio.NewReader(os.Stdin)
	ask := func(prompt string) (string, error) {
		fmt.Print(prompt)
		s, err := reader.ReadString('\n')
		return strings.TrimSpace(s), err
	}

	existing, _ := config.LoadMerged(rootFlags.configPath, false)
	names := existing.ProfileNames()

	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		for {
			n, err := ask("Profile name: ")
			if err != nil {
				return nil
			}
			if !profileNameRe.MatchString(n) {
				fmt.Println("  invalid: use letters, digits, '-' or '_'; must start with a letter or digit")
				continue
			}
			name = n
			break
		}
	} else if !profileNameRe.MatchString(name) {
		return fmt.Errorf("invalid profile name %q", name)
	}

	if slices.Contains(reservedSubcommands, name) {
		fmt.Printf("  note: %q shares a name with a subcommand; run it with `enver run %s -- <command>`\n", name, name)
	}

	extends := ""
	for {
		hint := ""
		if len(names) > 0 {
			hint = " (available: " + strings.Join(names, ", ") + ")"
		}
		e, err := ask("Extends (blank for none)" + hint + ": ")
		if err != nil {
			return nil
		}
		if e == "" {
			break
		}
		if !slices.Contains(names, e) {
			fmt.Printf("  no existing profile %q; leave blank or pick one above\n", e)
			continue
		}
		extends = e
		break
	}

	env := map[string]string{}
	fmt.Println("Environment variables (KEY=value, blank line to finish):")
	for {
		line, err := ask("  ")
		if err != nil {
			return nil
		}
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) == "" {
			fmt.Println("  skip: expected KEY=value")
			continue
		}
		k = strings.TrimSpace(k)
		if strings.ContainsAny(k, " \t") {
			fmt.Printf("  skip: invalid key %q (no spaces)\n", k)
			continue
		}
		env[k] = v
	}
	if len(env) == 0 && extends == "" {
		return fmt.Errorf("a profile needs at least one env var or an extends")
	}

	setDefault := false
	if existing.Default == "" {
		ans, _ := ask(fmt.Sprintf("Set %q as the default profile? [Y/n] ", name))
		setDefault = ans == "" || strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes")
	} else {
		ans, _ := ask(fmt.Sprintf("Set %q as the default? (current default: %s) [y/N] ", name, existing.Default))
		setDefault = strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes")
	}

	p := config.Profile{Extends: extends, Env: env}
	if err := config.UpsertProfile(cfgPath, name, p, setDefault); err != nil {
		return err
	}
	fmt.Printf("✓ wrote profile %q to %s\n", name, cfgPath)
	if setDefault {
		fmt.Printf("✓ set as default\n")
	}
	fmt.Printf("\nUse it: enver %s -- <command>\n", name)
	return nil
}
