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
// entries (with comments), the extends pointer, the unset list, whether it is
// the default, and a pending-delete flag. Nothing is written until commit.
type editState struct {
	name          string
	entries       []ui.EnvEntry
	extends       config.Extends
	unset         config.Unsets
	isDefault     bool
	deleteProfile bool

	// orig* snapshot the loaded profile for unsaved-change detection on the Done
	// row. origEntries is a copy, so in-place edits to entries never reach it.
	origExtends   config.Extends
	origIsDefault bool
	origEntries   []ui.EnvEntry
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
	return editState{
		name:          name,
		entries:       entries,
		extends:       prof.Extends,
		unset:         prof.Unset,
		isDefault:     isDefault,
		origExtends:   prof.Extends,
		origIsDefault: isDefault,
		origEntries:   append([]ui.EnvEntry(nil), entries...),
	}
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
	if len(s.entries) == 0 && len(s.extends) == 0 && len(s.unset) == 0 {
		return fmt.Errorf("a profile needs at least one env var, an extends, or an unset")
	}
	return nil
}

// dirty reports whether the working copy differs from the loaded profile: an
// added, removed, or modified entry, a changed extends, a toggled default, or a
// pending profile deletion.
func (s editState) dirty() bool {
	if !extendsEqual(s.extends, s.origExtends) || s.isDefault != s.origIsDefault || s.deleteProfile {
		return true
	}
	if len(s.entries) != len(s.origEntries) {
		return true
	}
	for i := range s.entries {
		if s.entries[i] != s.origEntries[i] {
			return true
		}
	}
	return false
}

func extendsEqual(a, b config.Extends) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// overrideKeySet returns the own keys that shadow a key contributed by the
// extends chain (parents only), so menuOptions and deleteVarOptions can mark
// actual overrides. The parent resolution blanks the profile's own env, so a
// shadowed key still appears here — unlike inheritedForState, which excludes
// own keys. A profile with no extends, or a pending extends that would cycle,
// yields nil.
func overrideKeySet(cfg config.Config, s editState) map[string]bool {
	if len(s.extends) == 0 {
		return nil
	}
	probe := probeConfig(cfg, s)
	tp := probe.Profiles[s.name]
	tp.Env = nil // resolve parents only
	probe.Profiles[s.name] = tp
	pr, err := probe.ResolveProfile(s.name)
	if err != nil {
		return nil
	}
	out := make(map[string]bool, len(s.entries))
	for _, e := range s.entries {
		if _, ok := pr.Env[e.Key]; ok {
			out[e.Key] = true
		}
	}
	return out
}

// overrideSeed builds the EnvEntry used to seed EnvCard when overriding an
// inherited variable: the inherited value (drawn from the menu's inherited set)
// plus its resolved comment, so the user sees what they are deviating from.
func overrideSeed(inherited []ui.EnvEntry, comments map[string]string, key string) ui.EnvEntry {
	seed := ui.EnvEntry{Key: key}
	for _, e := range inherited {
		if e.Key == key {
			seed.Value = e.Value
			break
		}
	}
	if c, ok := comments[key]; ok {
		seed.Comment = c
	}
	return seed
}

func (s editState) menuOptions(inherited []ui.EnvEntry, overrideKeys map[string]bool) []ui.Option {
	var opts []ui.Option
	for _, e := range s.entries {
		opt := ui.Option{Value: e.Key, Label: fmt.Sprintf("%s = %s", e.Key, e.Value)}
		if overrideKeys[e.Key] {
			opt.Icon = ui.IconOverride
		}
		opts = append(opts, opt)
	}
	for _, e := range inherited {
		opts = append(opts, ui.Option{Value: "inherited:" + e.Key, Icon: ui.IconInherited, Label: fmt.Sprintf("%s = %s", e.Key, e.Value)})
	}
	opts = append(opts, ui.Separator())
	opts = append(opts,
		ui.Option{Value: actionAdd, Icon: ui.IconAdd, Label: "Add variable"},
		ui.Option{Value: actionExtends, Icon: ui.IconExtends, Label: extendsLabel(s.extends)},
		ui.Option{Value: actionDefault, Icon: ui.IconDefault, Label: defaultLabel(s.isDefault)},
		ui.Option{Value: actionDeleteVar, Icon: ui.IconDeleteVar, Label: "Delete variable…"},
		ui.Option{Value: actionDeleteProfile, Icon: ui.IconDeleteProf, Label: "Delete profile…"},
		ui.Option{Value: actionDone, Icon: ui.IconDone, Label: doneLabel(s.dirty())},
	)
	return opts
}

func pickerTail() []ui.Option {
	return []ui.Option{
		ui.Separator(),
		{Value: actionCancel, Icon: ui.IconBack, Label: "Back", Action: true},
	}
}

// deleteVarOptions builds the delete picker: every own entry, with overrides
// (keys that also exist in the inherited set) marked so deleting them is clearly
// a revert to the inherited value rather than a removal, plus the Back tail.
func deleteVarOptions(s editState, overrideKeys map[string]bool) []ui.Option {
	own := make([]ui.Option, 0, len(s.entries)+2)
	for _, e := range s.entries {
		opt := ui.Option{Value: e.Key, Label: e.Key}
		if overrideKeys[e.Key] {
			opt.Icon = ui.IconOverride
			opt.Label = e.Key + " (→ inherited)"
		}
		own = append(own, opt)
	}
	return append(own, pickerTail()...)
}

func extendsLabel(extends config.Extends) string {
	if len(extends) == 0 {
		return "Change extends… (none)"
	}
	return fmt.Sprintf("Change extends… (%s)", strings.Join(extends, ", "))
}

func defaultLabel(isDefault bool) string {
	if isDefault {
		return "Clear default (current)"
	}
	return "Set as default"
}

// doneLabel flags the Done row when the working copy has unsaved changes, so
// the user can tell a commit would actually write (or that cancelling drops
// edits) without re-entering the editor.
func doneLabel(dirty bool) string {
	if dirty {
		return "Done • unsaved changes"
	}
	return "Done"
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
	ValidArgsFunction: completeProfileInTarget,
	RunE:              doEdit,
}

func doEdit(cmd *cobra.Command, args []string) error {
	if err := interactiveOnly("edit"); err != nil {
		return err
	}
	cfg, err := app.Load(appOpts())
	if err != nil {
		return err
	}
	path := writeTarget()
	targetCfg, _ := config.LoadFile(path)
	pickerCfg := cfg
	if globalFlags.global {
		pickerCfg, _ = config.LoadFile(config.GlobalPath(globalFlags.configPath))
	}
	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		picked, err := pickProfile(targetCfg, "Profile to edit", "")
		if err != nil || picked == "" {
			return nil
		}
		name = picked
	}
	prof, comments, isDefault, ok, err := config.ReadProfile(path, name)
	if err != nil {
		return err
	}
	if !ok {
		return notFoundInTarget(name, path)
	}
	wasDefault := isDefault

	s := newEditState(name, prof, comments, isDefault)
	for {
		inherited := inheritedForState(cfg, s)
		choice, err := ui.Select(editTitle(s), s.menuOptions(inherited, overrideKeySet(cfg, s)))
		if err != nil {
			// Only a cancel with pending edits is worth confirming; any other
			// error (no TTY, tea failure) or a clean cancel just exits.
			if err != ui.ErrCanceled || !s.dirty() {
				return nil
			}
			discard, cerr := ui.Confirm("Discard unsaved changes and exit?", false)
			if cerr != nil || !discard {
				continue
			}
			return nil
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
					fmt.Printf("\n✓ removed profile %q\n", s.name)
				} else {
					fmt.Printf("\n✓ updated profile %q\n", s.name)
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
				s.upsert(entry)
			case actionExtends:
				picked, err := ui.Select("Extends", append(extendsOptions(pickerCfg, name), pickerTail()...))
				if err != nil {
					continue
				}
				if picked == actionCancel {
					continue
				}
				if picked == "" {
					s.extends = nil
				} else {
					s.extends = config.Extends{picked}
				}
			case actionDefault:
				s.isDefault = !s.isDefault
			case actionDeleteVar:
				if len(s.entries) == 0 {
					fmt.Println("  no variables to delete")
					continue
				}
				picked, err := ui.MultiSelect("Variables to delete", deleteVarOptions(s, overrideKeySet(cfg, s)))
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
			probe := probeConfig(cfg, s)
			var comments map[string]string
			if pr, perr := probe.ResolveProfile(s.name); perr == nil {
				comments = pr.Comments
			}
			edited, err := ui.EnvCard(overrideSeed(inherited, comments, key))
			if err != nil {
				continue
			}
			if edited.Key = strings.TrimSpace(edited.Key); edited.Key == "" {
				continue
			}
			s.upsert(edited)
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
	p := config.Profile{Extends: s.extends, Unset: s.unset, Env: s.envMap(), Comments: s.commentsMap()}
	return config.WriteProfile(path, s.name, p, s.isDefault, !s.isDefault && wasDefault)
}

// commitValidate checks that the working copy can be committed: the non-empty
// invariant, and (when extends is set) that it would not form a cycle. It does
// not touch the filesystem.
func commitValidate(cfg config.Config, s editState) error {
	if err := s.canCommit(); err != nil {
		return err
	}
	if len(s.extends) > 0 {
		probe := config.Config{Default: cfg.Default, Profiles: make(map[string]config.Profile, len(cfg.Profiles))}
		for k, v := range cfg.Profiles {
			probe.Profiles[k] = v
		}
		tp := probe.Profiles[s.name]
		tp.Extends = s.extends
		probe.Profiles[s.name] = tp
		if _, err := probe.ResolveProfile(s.name); err != nil {
			return fmt.Errorf("extends %s would create a cycle", strings.Join(s.extends, ", "))
		}
	}
	return nil
}

func editTitle(s editState) string {
	if len(s.extends) == 0 {
		return fmt.Sprintf("Edit profile: %s", s.name)
	}
	return fmt.Sprintf("Edit profile: %s (extends %s)", s.name, strings.Join(s.extends, ", "))
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

// probeConfig returns a copy of cfg with profile s.name carrying the working
// extends and working own env, so resolution reflects uncommitted edits. Used by
// inheritedForState (values) and the override path (comments) so both track the
// same working extends.
func probeConfig(cfg config.Config, s editState) config.Config {
	probe := config.Config{Default: cfg.Default, Profiles: make(map[string]config.Profile, len(cfg.Profiles))}
	for k, v := range cfg.Profiles {
		probe.Profiles[k] = v
	}
	tp := probe.Profiles[s.name]
	tp.Extends = s.extends
	tp.Env = s.envMap()
	probe.Profiles[s.name] = tp
	return probe
}

// inheritedForState resolves the profile as it would exist if the working copy
// were committed — the working extends plus the working own env — and returns
// the inherited env entries contributed by the extends chain. It is recomputed
// on every menu redraw so a freshly picked extends (or deleting a variable that
// shadows an inherited one) shows up immediately, without committing and
// re-entering the editor. A pending extends that would form a cycle yields no
// inherited entries; commitValidate reports the cycle at commit time.
func inheritedForState(cfg config.Config, s editState) []ui.EnvEntry {
	probe := probeConfig(cfg, s)
	r, err := probe.ResolveProfile(s.name)
	if err != nil {
		return nil
	}
	return inheritedEntries(r.Env, s.envMap())
}

// extendsOptions builds the picker for changing extends: (none) plus every other
// profile (the profile itself excluded — self-extends is a cycle).
func extendsOptions(cfg config.Config, self string) []ui.Option {
	opts := []ui.Option{{Value: "", Label: "(none)"}}
	opts = append(opts, profileOptions(cfg, self)...)
	return opts
}
