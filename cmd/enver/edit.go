package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

const (
	actionAdd           = "action:add"
	actionExtends       = "action:extends"
	actionDefault       = "action:default"
	actionDeleteVar     = "action:delete-var"
	actionDeleteProfile = "action:delete-profile"
	actionDone          = "action:done"
	actionCancel        = "action:cancel"
)

// editState is the in-memory working copy of the profile being edited: own env
// entries (with comments), the extends pointer, whether it is the default, and a
// pending-delete flag. Nothing is written until commit.
type editState struct {
	name          string
	entries       []ui.EnvEntry
	extends       string
	isDefault     bool
	deleteProfile bool
}

func newEditState(name string, prof config.Profile, comments map[string]string, isDefault bool) editState {
	keys := make([]string, 0, len(prof.Env))
	for k := range prof.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	entries := make([]ui.EnvEntry, 0, len(keys))
	for _, k := range keys {
		entries = append(entries, ui.EnvEntry{Key: k, Value: prof.Env[k], Comment: comments[k]})
	}
	return editState{name: name, entries: entries, extends: prof.Extends, isDefault: isDefault}
}

func (s *editState) find(key string) (ui.EnvEntry, bool) {
	for _, e := range s.entries {
		if e.Key == key {
			return e, true
		}
	}
	return ui.EnvEntry{}, false
}

func (s *editState) upsert(e ui.EnvEntry) {
	e.Key = strings.TrimSpace(e.Key)
	for i := range s.entries {
		if s.entries[i].Key == e.Key {
			s.entries[i] = e
			return
		}
	}
	s.entries = append(s.entries, e)
}

func (s *editState) deleteKey(key string) {
	for i, e := range s.entries {
		if e.Key == key {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return
		}
	}
}

func (s editState) envMap() map[string]string {
	m := make(map[string]string, len(s.entries))
	for _, e := range s.entries {
		if e.Key != "" {
			m[e.Key] = e.Value
		}
	}
	return m
}

func (s editState) commentsMap() map[string]string {
	m := map[string]string{}
	for _, e := range s.entries {
		if e.Key != "" && e.Comment != "" {
			m[e.Key] = e.Comment
		}
	}
	return m
}

func (s editState) canCommit() error {
	if len(s.entries) == 0 && s.extends == "" {
		return fmt.Errorf("a profile needs at least one env var or an extends")
	}
	return nil
}

func (s editState) menuOptions(inherited []ui.EnvEntry) []ui.Option {
	var opts []ui.Option
	for _, e := range s.entries {
		opts = append(opts, ui.Option{Value: e.Key, Label: fmt.Sprintf("%s = %s", e.Key, e.Value)})
	}
	for _, e := range inherited {
		opts = append(opts, ui.Option{Value: "inherited:" + e.Key, Label: fmt.Sprintf("%s = %s (inherited)", e.Key, e.Value)})
	}
	opts = append(opts,
		ui.Option{Value: actionAdd, Label: "＋ Add variable"},
		ui.Option{Value: actionExtends, Label: extendsLabel(s.extends)},
		ui.Option{Value: actionDefault, Label: defaultLabel(s.isDefault)},
		ui.Option{Value: actionDeleteVar, Label: "🗑 Delete variable…"},
		ui.Option{Value: actionDeleteProfile, Label: "⚠ Delete profile…"},
		ui.Option{Value: actionDone, Label: "✓ Done"},
	)
	return opts
}

func pickerTail() []ui.Option {
	return []ui.Option{
		ui.Separator(),
		{Value: actionCancel, Label: "↩ Back"},
	}
}

func extendsLabel(extends string) string {
	if extends == "" {
		return "🔗 Change extends… (none)"
	}
	return fmt.Sprintf("🔗 Change extends… (%s)", extends)
}

func defaultLabel(isDefault bool) string {
	if isDefault {
		return "★ Clear default (current)"
	}
	return "★ Set as default"
}

// parseMenuChoice classifies a selection from menuOptions into a kind and key.
func parseMenuChoice(choice string, s editState) (kind, key string) {
	if strings.HasPrefix(choice, "inherited:") {
		return "inherited", strings.TrimPrefix(choice, "inherited:")
	}
	if _, ok := s.find(choice); ok {
		return "own", choice
	}
	return "action", choice
}

var editCmd = &cobra.Command{
	Use:               "edit [profile]",
	Short:             "Interactively edit a profile",
	Args:              cobra.MaximumNArgs(1),
	SilenceUsage:      true,
	SilenceErrors:     true,
	ValidArgsFunction: completeProfile,
	RunE:              doEdit,
}

func doEdit(cmd *cobra.Command, args []string) error {
	cfg, err := app.Load(appOpts())
	if err != nil {
		return err
	}
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		picked, err := pickProfile(cfg, "Profile to edit", "")
		if err != nil || picked == "" {
			return nil
		}
		name = picked
	}
	path := config.GlobalPath(globalFlags.configPath)
	prof, comments, isDefault, ok, err := config.ReadProfile(path, name)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	wasDefault := isDefault
	resolved, _, _ := cfg.ResolveProfile(name)
	inherited := inheritedEntries(resolved, prof.Env)

	s := newEditState(name, prof, comments, isDefault)
	for {
		choice, err := ui.Select(editTitle(s), s.menuOptions(inherited))
		if err != nil {
			return nil // esc / ctrl+c: cancel, nothing written
		}
		kind, key := parseMenuChoice(choice, s)
		switch kind {
		case "action":
			switch key {
			case actionDone:
				if err := commitEdit(path, cfg, s, wasDefault); err != nil {
					return err
				}
				if s.deleteProfile {
					fmt.Printf("✓ removed profile %q\n", s.name)
				} else {
					fmt.Printf("✓ updated profile %q\n", s.name)
				}
				return nil
			case actionAdd:
				entry, err := ui.EnvCard(ui.EnvEntry{})
				if err != nil {
					continue // sub-abort → back to menu
				}
				if entry.Key = strings.TrimSpace(entry.Key); entry.Key == "" {
					continue
				}
				if strings.ContainsAny(entry.Key, " \t") {
					fmt.Println("  skip: invalid key (no spaces)")
					continue
				}
				s.upsert(entry)
			case actionExtends:
				picked, err := ui.Select("Extends", append(extendsOptions(cfg, name), pickerTail()...))
				if err != nil {
					continue
				}
				if picked == actionCancel {
					continue
				}
				s.extends = picked
			case actionDefault:
				s.isDefault = !s.isDefault
			case actionDeleteVar:
				if len(s.entries) == 0 {
					fmt.Println("  no variables to delete")
					continue
				}
				own := make([]ui.Option, 0, len(s.entries)+2)
				for _, e := range s.entries {
					own = append(own, ui.Option{Value: e.Key, Label: e.Key})
				}
				own = append(own, pickerTail()...)
				picked, err := ui.MultiSelect("Variables to delete", own)
				if err != nil {
					continue
				}
				for _, key := range picked {
					if key == actionCancel {
						continue
					}
					s.deleteKey(key)
				}
			case actionDeleteProfile:
				if err := guardRemovable(cfg, name); err != nil {
					fmt.Println(" ", err)
					continue
				}
				ans, err := ui.Confirm(fmt.Sprintf("Delete profile %q?", name), false)
				if err != nil || !ans {
					continue
				}
				s.deleteProfile = true
				continue
			}
		case "own":
			cur, _ := s.find(key)
			edited, err := ui.EnvCard(cur)
			if err != nil {
				continue
			}
			if edited.Key = strings.TrimSpace(edited.Key); edited.Key == "" {
				continue // blank name cancels the edit of this var
			}
			if edited.Key != key {
				s.deleteKey(key)
			}
			s.upsert(edited)
		case "inherited":
			fmt.Println("  inherited variable — view only")
		}
	}
}

// commitEdit commits the working copy: a pending delete is honored first;
// otherwise it validates (extends cycle, non-empty invariant) and writes the
// profile. setDefault/clearDefault are derived from wasDefault vs the toggled
// state.
func commitEdit(path string, cfg config.Config, s editState, wasDefault bool) error {
	if s.deleteProfile {
		return config.DeleteProfile(path, s.name)
	}
	if err := commitValidate(cfg, s); err != nil {
		return err
	}
	p := config.Profile{Extends: s.extends, Env: s.envMap()}
	return config.WriteProfile(path, s.name, p, s.isDefault, !s.isDefault && wasDefault, s.commentsMap())
}

// commitValidate checks that the working copy can be committed: the non-empty
// invariant, and (when extends is set) that it would not form a cycle. It does
// not touch the filesystem.
func commitValidate(cfg config.Config, s editState) error {
	if err := s.canCommit(); err != nil {
		return err
	}
	if s.extends != "" {
		probe := config.Config{Default: cfg.Default, Profiles: make(map[string]config.Profile, len(cfg.Profiles))}
		for k, v := range cfg.Profiles {
			probe.Profiles[k] = v
		}
		tp := probe.Profiles[s.name]
		tp.Extends = s.extends
		probe.Profiles[s.name] = tp
		if _, _, err := probe.ResolveProfile(s.name); err != nil {
			return fmt.Errorf("extends %q would create a cycle", s.extends)
		}
	}
	return nil
}

func editTitle(s editState) string {
	if s.extends == "" {
		return fmt.Sprintf("Edit profile: %s", s.name)
	}
	return fmt.Sprintf("Edit profile: %s (extends %s)", s.name, s.extends)
}

// inheritedEntries returns resolved env keys that the profile does not define
// itself, for read-only display, sorted.
func inheritedEntries(resolved map[string]string, own map[string]string) []ui.EnvEntry {
	var out []ui.EnvEntry
	for k, v := range resolved {
		if _, ok := own[k]; !ok {
			out = append(out, ui.EnvEntry{Key: k, Value: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// extendsOptions builds the picker for changing extends: (none) plus every other
// profile (the profile itself excluded — self-extends is a cycle).
func extendsOptions(cfg config.Config, self string) []ui.Option {
	opts := []ui.Option{{Value: "", Label: "(none)"}}
	opts = append(opts, profileOptions(cfg, self)...)
	return opts
}
