package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/neiromaster/enver/internal/config"
)

var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

func doInitCmd(args []string) int {
	o := opts{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "enver: --config requires a value")
				return 2
			}
			o.configPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--config="):
			o.configPath = strings.TrimPrefix(a, "--config=")
		case strings.HasPrefix(a, "-") && len(a) > 1:
			fmt.Fprintf(os.Stderr, "enver: unknown flag %q\n", a)
			return 2
		default:
			if o.profile == "" {
				o.profile = a
			} else {
				fmt.Fprintf(os.Stderr, "enver: unexpected argument %q\n", a)
				return 2
			}
		}
	}
	return doInit(o)
}

func doInit(o opts) int {
	cfgPath := config.GlobalPath(o.configPath)

	reader := bufio.NewReader(os.Stdin)
	ask := func(prompt string) (string, error) {
		fmt.Print(prompt)
		s, err := reader.ReadString('\n')
		return strings.TrimSpace(s), err
	}

	existing, _ := config.LoadMerged(o.configPath, false)
	names := existing.ProfileNames()

	name := o.profile
	if name == "" {
		for {
			n, err := ask("Profile name: ")
			if err != nil {
				return 0 // EOF (e.g. Ctrl-D) — abort quietly
			}
			if !profileNameRe.MatchString(n) {
				fmt.Println("  invalid: use letters, digits, '-' or '_'; must start with a letter or digit")
				continue
			}
			name = n
			break
		}
	} else if !profileNameRe.MatchString(name) {
		fmt.Fprintf(os.Stderr, "enver: invalid profile name %q\n", name)
		return 2
	}

	extends := ""
	for {
		hint := ""
		if len(names) > 0 {
			hint = " (available: " + strings.Join(names, ", ") + ")"
		}
		e, err := ask("Extends (blank for none)" + hint + ": ")
		if err != nil {
			return 0
		}
		if e == "" {
			break
		}
		if !contains(names, e) {
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
			return 0
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
		fmt.Fprintln(os.Stderr, "enver: a profile needs at least one env var or an extends")
		return 2
	}

	setDefault := false
	if existing.Default == "" {
		ans, _ := ask(fmt.Sprintf("Set %q as the default profile? [Y/n] ", name))
		setDefault = ans == "" || strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes")
	} else {
		ans, _ := ask(fmt.Sprintf("Set %q as the default? (current default: %s) [y/N] ", name, existing.Default))
		setDefault = strings.EqualFold(ans, "y") || strings.EqualFold(ans, "yes")
	}

	if dir := filepath.Dir(cfgPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "enver: %v\n", err)
			return 1
		}
	}
	p := config.Profile{Extends: extends, Env: env}
	if err := config.UpsertProfile(cfgPath, name, p, setDefault); err != nil {
		fmt.Fprintf(os.Stderr, "enver: %v\n", err)
		return 1
	}

	fmt.Printf("✓ wrote profile %q to %s\n", name, cfgPath)
	if setDefault {
		fmt.Printf("✓ set as default\n")
	}
	fmt.Printf("\nUse it: enver %s -- <command>\n", name)
	return 0
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}