package main

import (
	"fmt"
	"maps"
	"slices"
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
	actionManageUnsets  = "action:manage-unsets"
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
	// row. origEntries and origUnset are copies, so in-place edits to them never
	// reach the snapshots.
	origExtends   config.Extends
	origIsDefault bool
	origEntries   []ui.EnvEntry
	origUnset     config.Unsets
}

func newEditState(name string, prof config.Profile, comments map[string]string, isDefault bool) editState {
	keys := slices.Sorted(maps.Keys(prof.Env))
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
		origUnset:     slices.Clone(prof.Unset),
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
// pending profile deletion. Unset fences compare order-insensitively, since
// order inside one layer's list is semantically inert.
func (s editState) dirty() bool {
	if !slices.Equal(s.extends, s.origExtends) || s.isDefault != s.origIsDefault || s.deleteProfile {
		return true
	}
	if !nameSetsEqual(s.unset, s.origUnset) {
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

// nameSetsEqual compares two name lists regardless of order.
func nameSetsEqual(a, b []string) bool {
	x := slices.Clone(a)
	y := slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// overrideKeySet returns the own keys that shadow a key contributed by the
// extends chain (parents only), so menuOptions and deleteVarOptions can mark
// actual overrides. The parent resolution blanks the profile's own env and its
// working fences (declared and carried) — who contributes a key says nothing
// about whether it is currently suppressed — so a shadowed key still appears
// here, unlike inheritedForState, which excludes own keys and honors unsets.
// A profile with no extends, or a pending extends that would cycle, yields nil.
func overrideKeySet(cfg config.Config, s editState) map[string]bool {
	if len(s.extends) == 0 {
		return nil
	}
	probe := probeConfig(cfg, s)
	tp := probe.Profiles[s.name]
	tp.Env = nil // resolve parents only
	probe.Profiles[s.name] = stripWorkingFences(tp)
	pr, err := probe.ResolveProfile(s.name)
	if err != nil {
		return nil
	}
	out := make(map[string]bool, len(s.entries))
	for _, e := range s.entries {
		if config.HasEnvKey(pr.Env, e.Key) {
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
		// Fence state outranks the override mark and reads as a faded row: the
		// variable stays listed so its suppression is visible where it happens.
		switch {
		case config.UnsetsHasKey(s.unset, e.Key):
			opt.Dim = true
		case overrideKeys[e.Key]:
			opt.Icon = ui.IconOverride
		}
		opts = append(opts, opt)
	}
	for _, e := range inherited {
		opt := ui.Option{Value: "inherited:" + e.Key, Icon: ui.IconInherited, Label: fmt.Sprintf("%s = %s", e.Key, e.Value)}
		if config.UnsetsHasKey(s.unset, e.Key) {
			opt.Dim = true
		}
		opts = append(opts, opt)
	}
	opts = append(opts, ui.Separator())
	opts = append(opts,
		ui.Option{Value: actionAdd, Icon: ui.IconAdd, Label: "Add variable"},
		ui.Option{Value: actionExtends, Icon: ui.IconExtends, Label: extendsLabel(s.extends)},
		ui.Option{Value: actionDefault, Icon: ui.IconDefault, Label: defaultLabel(s.isDefault)},
		ui.Option{Value: actionDeleteVar, Icon: ui.IconDeleteVar, Label: "Delete variable…"},
		ui.Option{Value: actionManageUnsets, Icon: ui.IconUnset, Label: manageUnsetsLabel(len(s.unset))},
		ui.Option{Value: actionDeleteProfile, Icon: ui.IconDeleteProf, Label: "Delete profile…"},
		ui.Option{Value: actionDone, Icon: ui.IconDone, Label: doneLabel(s.dirty())},
	)
	return opts
}

func manageUnsetsLabel(n int) string {
	if n == 0 {
		return "Manage unsets…"
	}
	return fmt.Sprintf("Manage unsets… (%d)", n)
}

func pickerTail() []ui.Option {
	return []ui.Option{
		ui.Separator(),
		{Value: actionCancel, Icon: ui.IconBack, Label: "Back", Action: true},
	}
}

// manageUnsetOptions builds the Manage-unsets picker: declared fences first
// (file order — including fences whose target resolves nowhere), then unset
// own entries, then unset inherited ones, plus the Back tail. Values are bare
// keys and unique across groups; MultiSelectChecked pre-marks the declared
// ones via the state's unset list.
func manageUnsetOptions(s editState, inherited []ui.EnvEntry) []ui.Option {
	opts := make([]ui.Option, 0, len(s.unset)+len(s.entries)+len(inherited)+2)
	for _, k := range s.unset {
		opts = append(opts, ui.Option{Value: k, Icon: ui.IconUnset, Label: k + " (declared here)"})
	}
	for _, e := range s.entries {
		if config.UnsetsHasKey(s.unset, e.Key) {
			continue
		}
		opts = append(opts, ui.Option{Value: e.Key, Label: e.Key + " (own)"})
	}
	for _, e := range inherited {
		if config.UnsetsHasKey(s.unset, e.Key) {
			continue
		}
		opts = append(opts, ui.Option{Value: e.Key, Label: e.Key + " (inherited)"})
	}
	return append(opts, pickerTail()...)
}

// definesKey reports whether key is among the profile's own entries by
// EnvKeyEqual semantics, so conflict detection folds case where resolution does.
func (s *editState) definesKey(key string) bool {
	for _, e := range s.entries {
		if config.EnvKeyEqual(e.Key, key) {
			return true
		}
	}
	return false
}

// dropUnsetKey strips every fence matching key (EnvKeyEqual semantics) and
// preserves the order of the survivors.
func dropUnsetKey(unsets config.Unsets, key string) config.Unsets {
	out := make(config.Unsets, 0, len(unsets))
	for _, u := range unsets {
		if !config.EnvKeyEqual(u, key) {
			out = append(out, u)
		}
	}
	return out
}

// planUnsets computes the next unset list from confirmed picker values without
// touching state: fences surviving this pass keep their file order, newly
// picked keys append in pick order with EnvKeyEqual-aware dedupe. Additions
// whose key is also an own env entry here are reported as conflicts — the
// same-layer pair validate warns about; the caller decides whether to keep or
// strip them.
func planUnsets(cur config.Unsets, picked []string, own func(string) bool) (config.Unsets, []string) {
	var next config.Unsets
	for _, k := range cur {
		if config.UnsetsHasKey(picked, k) {
			next = append(next, k)
		}
	}
	var conflicts []string
	for _, k := range picked {
		if config.UnsetsHasKey(cur, k) || config.UnsetsHasKey(next, k) {
			continue
		}
		next = append(next, k)
		if own(k) {
			conflicts = append(conflicts, k)
		}
	}
	return next, conflicts
}

// manageUnsets runs the unsets picker loop: an abort with pending toggles
// reopens the session instead of losing it silently, a full wipe confirms,
// and declining a same-layer conflict strips only the disputed additions —
// the rest of the batch survives.
func manageUnsets(s *editState, name string, inherited []ui.EnvEntry) {
	seed := slices.Clone(s.unset)
	for {
		picked, confirmed, err := ui.MultiSelectChecked(fmt.Sprintf("Manage unsets (%s)", name),
			manageUnsetOptions(*s, inherited), seed)
		if err != nil {
			return // sub-abort → back to menu
		}
		if !confirmed {
			if nameSetsEqual(picked, seed) {
				return // nothing pending to lose
			}
			stay, cerr := ui.Confirm("Unset toggles pending — keep editing them?", false)
			if cerr != nil || !stay {
				return
			}
			seed = picked // resume exactly where the checkboxes stood
			continue
		}

		if len(seed) > 0 && len(picked) == 0 {
			q := fmt.Sprintf("Remove all %d declared unset fences?", len(seed))
			ans, cerr := ui.Confirm(q, false)
			if cerr != nil || !ans {
				continue // back into the picker rather than wiping silently
			}
		}
		next, conflicts := planUnsets(s.unset, picked, s.definesKey)
		if len(conflicts) > 0 {
			q := fmt.Sprintf("Profile also defines %s — a same-layer define+unset pair enver validate warns about. Keep?",
				strings.Join(conflicts, ", "))
			ans, cerr := ui.Confirm(q, false)
			if cerr != nil || !ans {
				for _, k := range conflicts {
					next = dropUnsetKey(next, k)
				}
			}
		}
		s.unset = next
		return
	}
}

// settleDefineFence resolves a same-layer define+unset pair created by adding,
// renaming, or redefining onto a key that carries a declared fence: keeping is
// offered, declining lifts every matching fence so the definition takes
// effect. Mirrors the picker-side conflict guard.
func settleDefineFence(s *editState, key string) {
	if !config.UnsetsHasKey(s.unset, key) {
		return
	}
	q := fmt.Sprintf("Profile declares unset %s while defining it — a same-layer define+unset pair enver validate warns about. Keep the fence?", key)
	ans, err := ui.Confirm(q, false)
	if err != nil || !ans {
		s.unset = dropUnsetKey(s.unset, key)
	}
}

// deleteVarOptions builds the delete picker: every own entry, with fences
// marked above overrides — deleting a fenced key leaves its fence standing, so
// that must be visible where the deletion happens — and overrides (keys that
// also exist in the inherited set) marked so deleting them is clearly a revert
// to the inherited value rather than a removal, plus the Back tail.
func deleteVarOptions(s editState, overrideKeys map[string]bool) []ui.Option {
	own := make([]ui.Option, 0, len(s.entries)+2)
	for _, e := range s.entries {
		opt := ui.Option{Value: e.Key, Label: e.Key}
		switch {
		case config.UnsetsHasKey(s.unset, e.Key):
			opt.Icon = ui.IconUnset
			opt.Label += " · unset"
		case overrideKeys[e.Key]:
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
	targetCfg, err := config.LoadFile(path)
	if err != nil {
		return err
	}
	pickerCfg := cfg
	if globalFlags.global {
		pickerCfg, err = config.LoadFile(config.GlobalPath(globalFlags.configPath))
		if err != nil {
			return err
		}
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
		inherited := listedInheritedForState(cfg, s)
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
				settleDefineFence(&s, entry.Key)
				s.upsert(entry)
			case actionExtends:
				opts := append(seedOptions(s.extends, profileOptions(pickerCfg, name)), pickerTail()...)
				picked, confirmed, err := ui.MultiSelectOrdered("Edit extends for "+name, opts, s.extends)
				if err != nil || !confirmed {
					continue
				}
				if len(picked) == 0 {
					s.extends = nil
				} else {
					s.extends = config.Extends(picked)
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
				var deleted []string
				for _, key := range picked {
					if key == actionCancel {
						continue
					}
					if _, ok := s.find(key); ok {
						deleted = append(deleted, key)
					}
					s.deleteKey(key)
				}
				var stillFenced []string
				for _, k := range deleted {
					if config.UnsetsHasKey(s.unset, k) {
						stillFenced = append(stillFenced, k)
					}
				}
				slices.Sort(stillFenced)
				if len(stillFenced) > 0 {
					fmt.Printf("  %s deleted but its unset fence stands — lift it under Manage unsets\n",
						strings.Join(stillFenced, ", "))
				}
			case actionManageUnsets:
				manageUnsets(&s, name, inherited)
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
			settleDefineFence(&s, edited.Key)
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
			settleDefineFence(&s, edited.Key)
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
	if len(s.extends) > 0 && extendsCycles(cfg, s.name, s.extends) {
		return fmt.Errorf("extends %s would create a cycle", strings.Join(s.extends, ", "))
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
		if !config.HasEnvKey(own, k) {
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
	return probeWith(cfg, s.name, func(p *config.Profile) {
		p.Extends = s.extends
		p.Env = s.envMap()
		p.Unset = s.unset
	})
}

// inheritedForState resolves the profile as it would exist if the working copy
// were committed — the working extends plus the working own env — and returns
// the inherited env entries contributed by the extends chain. It is recomputed
// on every menu redraw so a freshly picked extends (or deleting a variable that
// shadows an inherited one) shows up immediately, without committing and
// re-entering the editor. A pending extends that would form a cycle yields no
// inherited entries; commitValidate reports the cycle at commit time.
func inheritedForState(cfg config.Config, s editState) []ui.EnvEntry {
	return inheritedViaProbe(cfg, s, true)
}

// listedInheritedForState is the menu-listing variant: standing fences do not
// remove entries here, so a suppressed key stays visible and selectable next to
// its live siblings, with dimming carrying what used to be an omission. The
// resolved view proper remains inheritedForState's business.
func listedInheritedForState(cfg config.Config, s editState) []ui.EnvEntry {
	return inheritedViaProbe(cfg, s, false)
}

// inheritedViaProbe resolves the working-copy profile and returns its
// parent-contributed keys minus own ones; honorFences decides whether declared
// unsets strip them from that set first.
func inheritedViaProbe(cfg config.Config, s editState, honorFences bool) []ui.EnvEntry {
	probe := probeConfig(cfg, s)
	if !honorFences {
		probe.Profiles[s.name] = stripWorkingFences(probe.Profiles[s.name])
	}
	r, err := probe.ResolveProfile(s.name)
	if err != nil {
		return nil
	}
	return inheritedEntries(r.Env, s.envMap())
}
