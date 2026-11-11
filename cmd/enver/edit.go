package main

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/neiromaster/enver/internal/envname"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

const (
	actionAdd           = "action:add"
	actionExtends       = "action:extends"
	actionDefault       = "action:default"
	actionSecrets       = "action:secrets"
	actionEncryptNow    = "action:encrypt-now"
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
	// encryptNow is the Secrets menu's request to run the encrypt step once after
	// a successful Done commit. It changes no entry, so it never reads as dirty.
	encryptNow bool

	// orig* snapshot the loaded profile for unsaved-change detection on the Done
	// row. origEntries and origUnset are copies, so in-place edits to them never
	// reach the snapshots.
	origExtends   config.Extends
	origIsDefault bool
	origEntries   []ui.EnvEntry
	origUnset     config.Unsets
}

// menuErrorAction classifies a failed menu Select. A cancel — ErrCanceled,
// however wrapped — is a normal exit: with pending edits the caller asks
// before discarding (ask=true), otherwise the session just ends cleanly.
// Any other error is a real failure and propagates instead of exiting 0.
func menuErrorAction(err error, dirty bool) (exitErr error, ask bool) {
	if !errors.Is(err, ui.ErrCanceled) {
		return err, false
	}
	if !dirty {
		return nil, false
	}
	return nil, true
}

// newEditState loads the working copy. A value sealed at rest is decrypted for
// the card and flagged, so the commit can seal it again; without a crypter the
// ciphertext stays as it is and the flag keeps that entry's card shut.
func newEditState(name string, prof config.Profile, comments map[string]string, isDefault bool, crypter *valueCrypter) (editState, error) {
	keys := slices.Sorted(maps.Keys(prof.Env))
	entries := make([]ui.EnvEntry, 0, len(keys))
	for _, k := range keys {
		secret, _ := envname.Get(prof.Marked, k)
		entry := ui.EnvEntry{Key: k, Value: prof.Env[k], Comment: comments[k], Secret: secret}
		if crypto.IsEncrypted(entry.Value) {
			entry.Encrypted = true
			if crypter != nil {
				plain, err := crypto.DecryptValue(entry.Value, crypter.Key)
				if err != nil {
					return editState{}, fmt.Errorf("decrypt %s: %w", k, err)
				}
				entry.Value = plain
			}
		}
		entries = append(entries, entry)
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
	}, nil
}

// cannotDecryptNote explains why an encrypted value's card stays shut: with no
// key the card could only show ciphertext, and submitting that text would
// overwrite the secret with it.
const cannotDecryptNote = "cannot decrypt: no key"

// editCardBlocked reports whether an entry's card must stay shut. The
// encryption flag alone is not the reason — a resolved crypter makes the value
// readable — so only the absence of a key blocks.
func editCardBlocked(e ui.EnvEntry, crypter *valueCrypter) bool {
	return e.Encrypted && crypter == nil
}

// find returns the working copy's entry for key, matched by envname.Equal: on
// Windows a case variant is the same variable, exactly as the loader sees it.
func (s *editState) find(key string) (ui.EnvEntry, bool) {
	for _, e := range s.entries {
		if envname.Equal(e.Key, key) {
			return e, true
		}
	}
	return ui.EnvEntry{}, false
}

// upsert replaces the entry key denotes, by envname.Equal — a byte-exact match
// on Windows would leave both spellings of one variable in the working copy,
// and the commit would then write a file the decoder rejects as a duplicate.
func (s *editState) upsert(e ui.EnvEntry) {
	e.Key = strings.TrimSpace(e.Key)
	for i := range s.entries {
		if envname.Equal(s.entries[i].Key, e.Key) {
			s.entries[i] = e
			return
		}
	}
	s.entries = append(s.entries, e)
}

// deleteKey drops the entry key denotes, by envname.Equal, so a case-variant
// rename or delete does not leave the old spelling behind to be written as a
// second entry of the same variable.
func (s *editState) deleteKey(key string) {
	for i, e := range s.entries {
		if envname.Equal(e.Key, key) {
			s.entries = append(s.entries[:i], s.entries[i+1:]...)
			return
		}
	}
}

func (s editState) envMap() map[string]string {
	m := make(map[string]string, len(s.entries))
	for _, e := range s.entries {
		if e.Key != "" {
			envname.Set(m, e.Key, e.Value)
		}
	}
	return m
}

// envForWrite builds the env mapping the commit persists. A value the card showed
// decrypted is sealed again under the crypter's key and era; everything else —
// including an entry still holding ciphertext because no key was available — is
// written unchanged. A crypter that cannot encrypt never gets here: commitEdit
// refuses it before composing the write.
func (s editState) envForWrite(crypter *valueCrypter) (map[string]string, error) {
	m := make(map[string]string, len(s.entries))
	for _, e := range s.entries {
		if e.Key == "" {
			continue
		}
		value := e.Value
		if e.Encrypted && crypter != nil && !crypto.IsEncrypted(value) {
			sealed, err := crypter.Encrypt(value)
			if err != nil {
				return nil, fmt.Errorf("encrypt %s: %w", e.Key, err)
			}
			value = sealed
		}
		envname.Set(m, e.Key, value)
	}
	return m, nil
}

// plaintextSecrets lists the keys that stay encrypted but now hold plaintext —
// the values a commit has to seal again to keep them secret at rest.
func (s editState) plaintextSecrets() []string {
	var keys []string
	for _, e := range s.entries {
		if e.Encrypted && !crypto.IsEncrypted(e.Value) {
			keys = append(keys, e.Key)
		}
	}
	return keys
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

// markMap is the working copy's secret-mark set, authoritative for every key it
// writes. It is non-nil even when empty: nil would tell the writer to fall back
// to the file, and the working copy has already decided.
func (s editState) markMap() map[string]bool {
	m := make(map[string]bool, len(s.entries))
	for _, e := range s.entries {
		if e.Key != "" && e.Secret {
			envname.Set(m, e.Key, true)
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
		if envname.Has(pr.Env, e.Key) {
			out[e.Key] = true
		}
	}
	return out
}

// overrideSeed builds the EnvEntry used to seed EnvCard when overriding an
// inherited variable: the inherited value (drawn from the menu's inherited set)
// plus its resolved comment, so the user sees what they are deviating from, and
// its secret mark, so deviating does not silently declassify the variable.
func overrideSeed(inherited []ui.EnvEntry, comments map[string]string, key string) ui.EnvEntry {
	seed := ui.EnvEntry{Key: key}
	for _, e := range inherited {
		if e.Key == key {
			seed.Value = e.Value
			seed.Secret = e.Secret
			seed.Encrypted = e.Encrypted
			break
		}
	}
	if c, ok := comments[key]; ok {
		seed.Comment = c
	}
	return seed
}

// addEntry commits one collected Add entry into the working copy, carrying the
// secret mark the copy already holds for that key. An entry that arrives marked
// keeps its mark: the value card cannot express the mark, so a true here is an
// explicit decision (the Secrets menu's inherited pick) while a false carries no
// information. The working copy's mark map is authoritative at write time, so an
// unmarked Add would declassify the key rather than let the writer fall back to
// the file or an ancestor.
func (s *editState) addEntry(entry ui.EnvEntry, inherited []ui.EnvEntry) {
	if !entry.Secret {
		entry.Secret = s.carriedMark(entry.Key, inherited)
	}
	if !entry.Encrypted {
		entry.Encrypted = s.carriedEncryption(entry.Key)
	}
	settleDefineFence(s, entry.Key)
	s.upsert(entry)
}

// carriedEncryption reports whether the copy already holds key as an encrypted
// entry. A collected Add entry cannot express the encryption state, so replacing
// an encrypted value must not write the typed text to the file in the clear.
// Inherited entries are resolved raw values and never carry the state.
func (s *editState) carriedEncryption(key string) bool {
	for _, e := range s.entries {
		if envname.Equal(e.Key, key) {
			return e.Encrypted
		}
	}
	return false
}

// carriedMark is the mark already in play for key: the own entry's, which is the
// closer authority, or else the inherited variable's. Same precedence
// MarksForWrite answers with, so adding a key never flips its mark.
func (s *editState) carriedMark(key string, inherited []ui.EnvEntry) bool {
	for _, e := range s.entries {
		if envname.Equal(e.Key, key) {
			return e.Secret
		}
	}
	for _, e := range inherited {
		if envname.Equal(e.Key, key) {
			return e.Secret
		}
	}
	return false
}

// maskedLabel renders one variable row: the key spelled with the mark it
// carries, and the value masked exactly as the menu listing masks it, so no
// picker reveals a value another surface hides. The mask lookup keeps the
// runtime name; the mark is display syntax only.
func maskedLabel(e ui.EnvEntry) string {
	return fmt.Sprintf("%s = %s", envname.JoinMarked(e.Key, e.Secret), config.MaskValue(e.Value, config.ShouldMask(e.Key, e.Value, e.Secret, e.Encrypted)))
}

func (s editState) menuOptions(inherited []ui.EnvEntry, overrideKeys map[string]bool) []ui.Option {
	var opts []ui.Option
	for _, e := range s.entries {
		opt := ui.Option{Value: e.Key, Label: maskedLabel(e)}
		// Fence state outranks the override mark and reads as a faded row: the
		// variable stays listed so its suppression is visible where it happens.
		switch {
		case envname.MatchesAny(s.unset, e.Key):
			opt.Dim = true
		case overrideKeys[e.Key]:
			opt.Icon = ui.IconOverride
		}
		opts = append(opts, opt)
	}
	for _, e := range inherited {
		opt := ui.Option{Value: "inherited:" + e.Key, Icon: ui.IconInherited, Label: maskedLabel(e)}
		if envname.MatchesAny(s.unset, e.Key) {
			opt.Dim = true
		}
		opts = append(opts, opt)
	}
	opts = append(opts, ui.Separator())
	opts = append(opts,
		ui.Option{Value: actionAdd, Icon: ui.IconAdd, Label: "Add variable"},
		ui.Option{Value: actionExtends, Icon: ui.IconExtends, Label: extendsLabel(s.extends)},
		ui.Option{Value: actionDefault, Icon: ui.IconDefault, Label: defaultLabel(s.isDefault)},
		ui.Option{Value: actionSecrets, Icon: ui.IconSecret, Label: "Secrets…"},
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
// ones via the state's unset list. Declared rows hold fence spellings, so only
// variable rows spell the mark.
func manageUnsetOptions(s editState, inherited []ui.EnvEntry) []ui.Option {
	opts := make([]ui.Option, 0, len(s.unset)+len(s.entries)+len(inherited)+2)
	for _, k := range s.unset {
		opts = append(opts, ui.Option{Value: k, Icon: ui.IconUnset, Label: k + " (declared here)"})
	}
	for _, e := range s.entries {
		if envname.MatchesAny(s.unset, e.Key) {
			continue
		}
		opts = append(opts, ui.Option{Value: e.Key, Label: envname.JoinMarked(e.Key, e.Secret) + " (own)"})
	}
	for _, e := range inherited {
		if envname.MatchesAny(s.unset, e.Key) {
			continue
		}
		opts = append(opts, ui.Option{Value: e.Key, Label: envname.JoinMarked(e.Key, e.Secret) + " (inherited)"})
	}
	return append(opts, pickerTail()...)
}

// definesKey reports whether key is among the profile's own entries by
// envname.Equal semantics, so conflict detection folds case where resolution does.
func (s *editState) definesKey(key string) bool {
	for _, e := range s.entries {
		if envname.Equal(e.Key, key) {
			return true
		}
	}
	return false
}

// dropUnsetKey strips every fence matching key (envname.Equal semantics) and
// preserves the order of the survivors.
func dropUnsetKey(unsets config.Unsets, key string) config.Unsets {
	out := make(config.Unsets, 0, len(unsets))
	for _, u := range unsets {
		if !envname.Equal(u, key) {
			out = append(out, u)
		}
	}
	return out
}

// planUnsets computes the next unset list from confirmed picker values without
// touching state: fences surviving this pass keep their file order, newly
// picked keys append in pick order with envname.Equal-aware dedupe. Additions
// whose key is also an own env entry here are reported as conflicts — the
// same-layer pair validate warns about; the caller decides whether to keep or
// strip them.
func planUnsets(cur config.Unsets, picked []string, own func(string) bool) (config.Unsets, []string) {
	var next config.Unsets
	for _, k := range cur {
		if envname.MatchesAny(picked, k) {
			next = append(next, k)
		}
	}
	var conflicts []string
	for _, k := range picked {
		if envname.MatchesAny(cur, k) || envname.MatchesAny(next, k) {
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

// secretsOptions builds the Secrets picker: own entries in working order, then
// the inherited keys the copy does not own, sorted, then the Encrypt-now row and
// the Back tail. Values are runtime names, so the mark never enters a key. A
// fenced key stays listed and fades, carrying the state the menu shows: marking
// a value resolution suppresses must not look like marking a live one.
func secretsOptions(s editState, inherited []ui.EnvEntry) []ui.Option {
	opts := make([]ui.Option, 0, len(s.entries)+len(inherited)+4)
	for _, e := range s.entries {
		opts = append(opts, ui.Option{
			Value: e.Key,
			Icon:  secretIcon(e.Secret),
			Label: maskedLabel(e),
			Dim:   envname.MatchesAny(s.unset, e.Key),
		})
	}
	own := s.envMap()
	rest := make([]ui.EnvEntry, 0, len(inherited))
	for _, e := range inherited {
		if !envname.Has(own, e.Key) {
			rest = append(rest, e)
		}
	}
	slices.SortFunc(rest, func(a, b ui.EnvEntry) int { return cmp.Compare(a.Key, b.Key) })
	for _, e := range rest {
		icon := secretIcon(e.Secret)
		if icon == "" {
			icon = ui.IconInherited
		}
		opts = append(opts, ui.Option{
			Value: e.Key,
			Icon:  icon,
			Label: maskedLabel(e) + " (inherited)",
			Dim:   envname.MatchesAny(s.unset, e.Key),
		})
	}
	opts = append(opts,
		ui.Separator(),
		ui.Option{Value: actionEncryptNow, Label: "Encrypt now"},
	)
	return append(opts, pickerTail()...)
}

// secretIcon is the mark's glyph: the picker shows the state it is about to
// change, and only a marked key earns the mark.
func secretIcon(secret bool) string {
	if secret {
		return ui.IconSecret
	}
	return ""
}

// secretsChecked seeds the picker from the working copy: the marks in play, plus
// the Encrypt-now row when that step is already scheduled, so a reopened menu
// shows the truth and confirming it keeps the request.
func secretsChecked(s editState) []string {
	checked := slices.Sorted(maps.Keys(s.markMap()))
	if s.encryptNow {
		checked = append(checked, actionEncryptNow)
	}
	return checked
}

// applySecretsPick folds a confirmed selection into the working copy. Own entries
// take the picked mark set in place — routing them through addEntry would let the
// carried mark restore exactly the mark being cleared. A picked inherited key
// becomes an own marked entry seeded with the value and comment the picker showed.
// The Encrypt-now row only schedules the post-commit step: nothing is written here.
func applySecretsPick(s *editState, picked []string, inherited []ui.EnvEntry, comments map[string]string) {
	on := make(map[string]bool, len(picked))
	for _, v := range picked {
		on[v] = true
	}
	s.encryptNow = on[actionEncryptNow]
	for i := range s.entries {
		s.entries[i].Secret = on[s.entries[i].Key]
	}
	for _, e := range inherited {
		if !on[e.Key] || s.definesKey(e.Key) {
			continue
		}
		seed := overrideSeed(inherited, comments, e.Key)
		seed.Secret = true
		s.addEntry(seed, inherited)
	}
}

// secrets runs the Secrets picker. A cancel or a Back row abandons the session
// without touching the working copy; only a confirmed selection stages marks,
// and the Done commit is what writes them.
func secrets(s *editState, inherited []ui.EnvEntry, cfg config.Config) {
	picked, confirmed, err := ui.MultiSelectChecked("Secrets", secretsOptions(*s, inherited), secretsChecked(*s))
	if err != nil || !confirmed {
		return
	}
	applySecretsPick(s, picked, inherited, resolvedComments(cfg, *s))
}

// resolvedComments returns the comments the working copy would resolve to, so a
// value taken from an inherited row keeps the comment that explains it.
func resolvedComments(cfg config.Config, s editState) map[string]string {
	pr, err := probeConfig(cfg, s).ResolveProfile(s.name)
	if err != nil {
		return nil
	}
	return pr.Comments
}

// settleDefineFence resolves a same-layer define+unset pair created by adding,
// renaming, or redefining onto a key that carries a declared fence: keeping is
// offered, declining lifts every matching fence so the definition takes
// effect. Mirrors the picker-side conflict guard.
func settleDefineFence(s *editState, key string) {
	if !envname.MatchesAny(s.unset, key) {
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
		label := envname.JoinMarked(e.Key, e.Secret)
		opt := ui.Option{Value: e.Key, Label: label}
		switch {
		case envname.MatchesAny(s.unset, e.Key):
			opt.Icon = ui.IconUnset
			opt.Label = label + " · unset"
		case overrideKeys[e.Key]:
			opt.Icon = ui.IconOverride
			opt.Label = label + " (→ inherited)"
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
	path, targetCfg, err := loadTarget()
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
	prof, isDefault, ok, err := config.ReadProfile(path, name)
	if err != nil {
		return err
	}
	if !ok {
		return notFoundInTarget(name, path)
	}
	wasDefault := isDefault

	crypter, err := crypterForProfile(path, prof)
	if err != nil {
		return err
	}
	s, err := newEditState(name, prof, prof.Comments, isDefault, crypter)
	if err != nil {
		return err
	}
	for {
		inherited := listedInheritedForState(cfg, s)
		choice, err := ui.Select(editTitle(s), s.menuOptions(inherited, overrideKeySet(cfg, s)))
		if err != nil {
			exitErr, ask := menuErrorAction(err, s.dirty())
			if exitErr != nil {
				return exitErr
			}
			if !ask {
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
				if err := commitEdit(path, cfg, s, wasDefault, crypter); err != nil {
					fmt.Fprintf(os.Stderr, "\n%v\n", err)
					continue
				}
				if s.deleteProfile {
					fmt.Printf("\n✓ removed profile %q\n", s.name)
					return nil
				}
				fmt.Printf("\n✓ updated profile %q\n", s.name)
				if s.encryptNow {
					if err := encryptMarked(path, s.name); err != nil {
						return fmt.Errorf("profile %q saved, but encrypting its marked values failed: %w", s.name, err)
					}
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
				s.addEntry(entry, inherited)
			case actionExtends:
				opts := append(seedOptions(s.extends, profileOptions(pickerCfg, name)), pickerTail()...)
				picked, confirmed, err := ui.MultiSelectOrdered("Edit extends for "+name, opts, s.extends)
				if err != nil || !confirmed {
					continue
				}
				if err := applyExtendsPick(cfg, name, &s, picked); err != nil {
					fmt.Fprintf(os.Stderr, "\n%v\n", err)
				}
			case actionDefault:
				s.isDefault = !s.isDefault
			case actionSecrets:
				secrets(&s, inherited, cfg)
			case actionDeleteVar:
				if len(s.entries) == 0 {
					fmt.Println("  no variables to delete")
					continue
				}
				seed := []string{}
				for {
					picked, confirmed, err := ui.MultiSelectChecked("Variables to delete",
						deleteVarOptions(s, overrideKeySet(cfg, s)), seed)
					if err != nil {
						break // sub-abort → back to menu
					}
					if !confirmed {
						if slices.Equal(picked, seed) {
							break
						}
						stay, cerr := ui.Confirm("Deletions pending — keep selection?", false)
						if cerr != nil || !stay {
							break
						}
						seed = picked
						continue
					}
					var deleted []string
					for _, key := range picked {
						if _, ok := s.find(key); ok {
							deleted = append(deleted, key)
						}
						s.deleteKey(key)
					}
					var stillFenced []string
					for _, k := range deleted {
						if envname.MatchesAny(s.unset, k) {
							stillFenced = append(stillFenced, k)
						}
					}
					slices.Sort(stillFenced)
					if len(stillFenced) > 0 {
						fmt.Printf("  %s deleted but its unset fence stands — lift it under Manage unsets\n",
							strings.Join(stillFenced, ", "))
					}
					break
				}
			case actionManageUnsets:
				manageUnsets(&s, name, inherited)
			case actionDeleteProfile:
				if err := guardRemovable(cfg, name, "remove"); err != nil {
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
			if editCardBlocked(cur, crypter) {
				fmt.Println("  " + cannotDecryptNote)
				continue
			}
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
			edited, err := ui.EnvCard(overrideSeed(inherited, resolvedComments(cfg, s), key))
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

// applyExtendsPick sets the working copy's extends from a confirmed picker
// result, rejecting a chain that would cycle before any state changes — the
// same verdict commitValidate would give, surfaced while the menu is still
// open instead of at Done.
func applyExtendsPick(cfg config.Config, name string, s *editState, picked []string) error {
	if err := extendsCycleError(cfg, name, picked); err != nil {
		return err
	}
	if len(picked) == 0 {
		s.extends = nil
	} else {
		s.extends = config.Extends(picked)
	}
	return nil
}

// commitEdit commits the working copy: a pending delete is honored first;
// otherwise it validates (extends cycle, non-empty invariant) and writes the
// profile, secret marks included as the working copy decided them and encrypted
// values sealed again. setDefault/clearDefault are derived from wasDefault vs the
// toggled state. A value that must be sealed but cannot be — no key at all, or a
// key without a salt — fails before the file is touched, since writing it as it
// stands would put the secret on disk in the clear.
func commitEdit(path string, cfg config.Config, s editState, wasDefault bool, crypter *valueCrypter) error {
	if s.deleteProfile {
		return config.DeleteProfile(path, s.name)
	}
	if err := commitValidate(cfg, s); err != nil {
		return err
	}
	if keys := s.plaintextSecrets(); len(keys) > 0 {
		switch {
		case crypter == nil:
			return fmt.Errorf("refusing to write %s in plaintext: no key is available (run `enver keygen`, or set --key/ENVER_KEY)",
				strings.Join(keys, ", "))
		case crypter.Salt == nil:
			return errSaltlessCrypter
		}
	}
	env, err := s.envForWrite(crypter)
	if err != nil {
		return err
	}
	p := config.Profile{Extends: s.extends, Unset: s.unset, Env: env, Comments: s.commentsMap(), Marked: s.markMap()}
	return config.WriteProfile(path, s.name, p, s.isDefault, !s.isDefault && wasDefault)
}

// commitValidate checks that the working copy can be committed: the non-empty
// invariant, and (when extends is set) that it would not form a cycle. It does
// not touch the filesystem.
func commitValidate(cfg config.Config, s editState) error {
	if err := s.canCommit(); err != nil {
		return err
	}
	return extendsCycleError(cfg, s.name, s.extends)
}

// valueCrypter is the key and KDF era a write seals values under. One value
// carries one key, so both travel together and no caller can pair a key with an
// era it was not derived under.
type valueCrypter struct {
	Key    []byte
	Salt   []byte
	Params crypto.Argon2Params
}

// errSaltlessCrypter reports a crypter that can decrypt but not encrypt: the key
// came from ENVER_KEY, which carries no salt to seal new values under.
var errSaltlessCrypter = errors.New("this key cannot encrypt (ENVER_KEY carries no salt)")

// Encrypt seals plain into an enc:v3: value under the crypter's key and era.
func (c *valueCrypter) Encrypt(plain string) (string, error) {
	return crypto.EncryptValueWithParams(plain, c.Key, c.Salt, c.Params)
}

// crypterForProfile resolves the crypter the editor needs for a profile's own
// encrypted values. A profile with none needs no key, so opening it neither
// resolves nor prompts for one. An absent key — and a declined passphrase, which
// is the same absence — leaves the ciphertext in place; a wrong key is a
// misconfiguration and propagates.
func crypterForProfile(path string, prof config.Profile) (*valueCrypter, error) {
	if !slices.ContainsFunc(slices.Collect(maps.Values(prof.Env)), crypto.IsEncrypted) {
		return nil, nil
	}
	crypter, err := crypterFor(path)
	if errors.Is(err, app.ErrNoKey) || errors.Is(err, ui.ErrCanceled) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return crypter, nil
}

// crypterFor resolves the key and era for writing encrypted values in path. The
// era comes from the file itself: pairing CurrentParams with an existing salt
// would derive a different key and strand every value under it.
func crypterFor(path string) (*valueCrypter, error) {
	fileSalt, eraParams, sample, err := config.FirstSaltAndSample(path)
	if err != nil {
		return nil, err
	}
	key, salt, err := app.ResolveKeyOrPrompt(appOpts(), func() ([]byte, crypto.Argon2Params, string, error) {
		return fileSalt, eraParams, sample, nil
	})
	if err != nil {
		return nil, err
	}
	params := eraParams
	if fileSalt == nil { // no encrypted value yet: nothing to match
		params = crypto.CurrentParams
	}
	return &valueCrypter{Key: key, Salt: salt, Params: params}, nil
}

// encryptMarked runs the Secrets menu's Encrypt-now request against the file the
// Done commit just wrote: the commit put the mark into the file, and the mark is
// what ShouldEncrypt keys off. A key that cannot encrypt — none at all, a
// declined passphrase prompt, or ENVER_KEY's saltless key — leaves the marks in
// place and only notes the plaintext, since the mark is a hint and never a
// condition for saving.
func encryptMarked(path, profile string) error {
	c, err := crypterFor(path)
	if errors.Is(err, app.ErrNoKey) || errors.Is(err, ui.ErrCanceled) {
		fmt.Printf("  %s — no key to encrypt with; the marks are saved\n", markNote)
		return nil
	}
	if err != nil {
		return err
	}
	if c.Salt == nil {
		fmt.Printf("  %s — ENVER_KEY carries no salt; the marks are saved\n", markNote)
		return nil
	}
	n, err := config.EncryptFile(path, c.Key, c.Salt, profile, false)
	if err != nil {
		return err
	}
	fmt.Printf("✓ encrypted %d value(s) in %s\n", n, path)
	return nil
}

func editTitle(s editState) string {
	if len(s.extends) == 0 {
		return fmt.Sprintf("Edit profile: %s", s.name)
	}
	return fmt.Sprintf("Edit profile: %s (extends %s)", s.name, strings.Join(s.extends, ", "))
}

// inheritedEntries returns resolved env keys that the profile does not define
// itself, for read-only display, sorted, each with the secret mark its winning
// entry carried.
func inheritedEntries(resolved map[string]string, marked map[string]bool, own map[string]string) []ui.EnvEntry {
	var out []ui.EnvEntry
	for k, v := range resolved {
		if !envname.Has(own, k) {
			secret, _ := envname.Get(marked, k)
			out = append(out, ui.EnvEntry{Key: k, Value: v, Secret: secret})
		}
	}
	slices.SortFunc(out, func(a, b ui.EnvEntry) int { return cmp.Compare(a.Key, b.Key) })
	return out
}

// probeConfig returns a copy of cfg with profile s.name carrying the working
// extends, working own env, and working marks, so resolution reflects
// uncommitted edits. Used by inheritedForState (values) and the override path
// (comments) so both track the same working extends. Inherited rows take their
// marks from the ancestors and own keys are dropped from that set, so the marks
// carried here answer only a consumer reading own-key marks.
func probeConfig(cfg config.Config, s editState) config.Config {
	return probeWith(cfg, s.name, func(p *config.Profile) {
		p.Extends = s.extends
		p.Env = s.envMap()
		p.Marked = s.markMap()
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
	return inheritedEntries(r.Env, r.Marked, s.envMap())
}
