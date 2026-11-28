package main

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/neiromaster/enver/internal/app"
	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"github.com/neiromaster/enver/internal/envname"
	"github.com/neiromaster/enver/internal/ui"
	"github.com/spf13/cobra"
)

var profileNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*(/[A-Za-z0-9][A-Za-z0-9_-]*)?$`)

func validateProfileName(name string) error {
	if !profileNameRe.MatchString(name) {
		return fmt.Errorf("invalid profile name %q", name)
	}
	return nil
}

// promptProfileName reads a profile name interactively under the given title,
// re-prompting on invalid input. It returns the trimmed name and ok=true, or
// ok=false when the input prompt fails (for example EOF), in which case the
// caller aborts silently.
func promptProfileName(title string, input func(string) (string, error)) (string, bool) {
	for {
		n, err := input(title)
		if err != nil {
			return "", false
		}
		n = strings.TrimSpace(n)
		if err := validateProfileName(n); err != nil {
			fmt.Println(`  invalid: use letters, digits, '-' or '_', with at most one '/' splitting remote-name/profile; each segment must start with a letter or digit`)
			continue
		}
		return n, true
	}
}

// parentEnvFor resolves the picked extends chain as the inherited backdrop for
// the collecting summary: parents merge left-to-right, while the target
// profile's own env and working fences are stripped, so its own keys never
// read as inherited and a declared unset cannot hide a parent key from the
// summary. The marks come from the same resolution as the values, so the
// summary masks what a reader would mask. When the chain does not resolve, each
// parent's subtree resolves on its own — one broken ancestor must not blank
// every healthy parent — and the healthy ones still merge in pick order, a later
// parent's mark travelling with its value, while the broken ones come back as
// warnings for the caller to print.
func parentEnvFor(cfg config.Config, self string, extends config.Extends) (map[string]string, map[string]bool, []string) {
	if len(extends) == 0 {
		return map[string]string{}, nil, nil
	}
	blankSelf := func(p *config.Profile) {
		p.Env = nil
		*p = stripWorkingFences(*p)
	}
	if r, err := probeWith(cfg, self, func(p *config.Profile) {
		p.Extends = extends
		blankSelf(p)
	}).ResolveProfile(self); err == nil {
		return r.Env, r.Marked, nil
	}
	backdrop := map[string]string{}
	var marked map[string]bool
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
		for k, v := range br.Env {
			backdrop[k] = v
			if br.Marked[k] {
				if marked == nil {
					marked = map[string]bool{}
				}
				marked[k] = true
			} else {
				delete(marked, k)
			}
		}
	}
	return backdrop, marked, warns
}

// remoteBackdrop resolves the remote-provided name as the inherited base for a
// patch bootstrap. The saved copy lands as the top user layer, so the backdrop is
// the merged view of the name — remote profile under any user copies of its
// parents, which compose into the effective base.
// Resolution rides the display seam the show command uses: an undecryptable
// remote value degrades to a per-key note instead of failing, which is the only
// honest answer for ciphertext its author may have written under another key.
// The degraded keys come back with the note in place of their value (a marked
// value would mask the note, so the mark is dropped for display only) and one
// warning per key. A resolution failure is a note too: the collector still
// opens, just without a base, rather than refusing a patch the writer accepts.
func remoteBackdrop(merged config.Config, name string) (map[string]string, map[string]bool, []string) {
	if _, ok := config.RemoteName(merged.ProfileLayer[name]); !ok {
		return map[string]string{}, nil, nil
	}
	opts := appOpts()
	opts.TolerateMissingKey = true
	opts.TolerateRemoteWrongKey = true
	r, err := app.ResolveConfig(merged, []string{name}, opts)
	if err != nil {
		return map[string]string{}, nil, []string{fmt.Sprintf("note: remote base for %q is unresolvable (%v) — its inherited keys are hidden", name, err)}
	}
	env := maps.Clone(r.Env)
	marked := maps.Clone(r.Marked)
	var warns []string
	for _, k := range slices.Sorted(maps.Keys(r.Undecryptable)) {
		reason := r.Undecryptable[k]
		env[k] = "(cannot decrypt: " + reason + ")"
		delete(marked, k)
		warns = append(warns, fmt.Sprintf("note: %s cannot be decrypted from the remote (%s); its value stays hidden unless you override it", k, reason))
	}
	return env, marked, warns
}

// buildProfile composes the profile add writes: env keyed by runtime name, and
// the marks the entries carry — a typed ~ or the card's carried flag — which the
// caller merges with the marks the write implies.
func buildProfile(extends config.Extends, entries []ui.EnvEntry) (config.Profile, map[string]string) {
	env := make(map[string]string, len(entries))
	comments := map[string]string{}
	var marked map[string]bool
	for _, e := range entries {
		name, typed := envname.SplitMarked(e.Key)
		env[name] = e.Value
		if e.Comment != "" {
			comments[name] = e.Comment
		}
		if typed || e.Secret {
			if marked == nil {
				marked = map[string]bool{}
			}
			envname.Set(marked, name, true)
		}
	}
	return config.Profile{Extends: extends, Env: env, Marked: marked}, comments
}

// mergeMarks folds the marks the user typed over the ones the write implies. The
// typed mark is the closest authority for its own key, so it wins; a key the
// user typed plainly carries no information, so the implied answer stands. The
// two answers are merged for exactly that reason — letting either one win
// outright would declassify a key the other marks.
func mergeMarks(implied, typed map[string]bool) map[string]bool {
	out := maps.Clone(implied)
	for k, on := range typed {
		if !on {
			continue
		}
		if out == nil {
			out = map[string]bool{}
		}
		envname.Set(out, k, true)
	}
	return out
}

// buildSummary builds the display summary for the collecting env-card: own entries
// (marked Override when they shadow an inherited key) in insertion order, then the
// inherited keys not defined as own (sorted). Values are masked via config.MaskValue
// under the mark in play for the key — the typed one for an own entry, else the
// one the write keeps (writeMarks) or, for an inherited row, the parent's — so the
// summary hides what the finished profile would hide. An own row's untyped half
// comes from writeMarks, the answer the commit itself uses: the target profile's
// stored mark for a key it already holds, the picked chain's for a new one.
// Reading it from the write rather than re-deriving it is what keeps the preview
// from disagreeing with the file, in either direction. entries is not mutated.
func buildSummary(entries []ui.EnvEntry, parentEnv map[string]string, parentMarked, writeMarks map[string]bool) []ui.SummaryEntry {
	out := make([]ui.SummaryEntry, 0, len(entries)+len(parentEnv))
	own := make(map[string]bool, len(entries))
	for _, e := range entries {
		own[e.Key] = true
		kind := ui.EntryAdded
		if _, ok := parentEnv[e.Key]; ok {
			kind = ui.EntryOverride
		}
		marked := e.Secret || writeMarks[e.Key]
		out = append(out, ui.SummaryEntry{
			Key:   envname.JoinMarked(e.Key, marked),
			Value: config.MaskValue(e.Value, config.ShouldMask(e.Value, marked, crypto.IsEncrypted(e.Value))),
			Kind:  kind,
		})
	}
	inherited := make([]string, 0, len(parentEnv))
	for k := range parentEnv {
		if !own[k] {
			inherited = append(inherited, k)
		}
	}
	slices.Sort(inherited)
	for _, k := range inherited {
		out = append(out, ui.SummaryEntry{
			Key:   envname.JoinMarked(k, parentMarked[k]),
			Value: config.MaskValue(parentEnv[k], config.ShouldMask(parentEnv[k], parentMarked[k], crypto.IsEncrypted(parentEnv[k]))),
			Kind:  ui.EntryInherited,
		})
	}
	return out
}

// addMarks answers the secret marks for the entries add writes: the shared
// write-mark helper with add's picked extends, which the write sets outright,
// and the working fences, which an upsert never clears. The query is keyed by
// the runtime names the write actually writes.
func addMarks(under []config.Layer, target config.Config, name string, extends config.Extends, entries []ui.EnvEntry) map[string]bool {
	keys := make([]string, 0, len(entries))
	for _, e := range entries {
		key, _ := envname.SplitMarked(e.Key)
		keys = append(keys, key)
	}
	return app.WriteMarks(app.WriteQuery{
		Under:          under,
		Target:         target,
		TargetIsGlobal: globalFlags.global,
		Name:           name,
		Extends:        extends,
		Keys:           keys,
	})
}

// commitAdd writes the collected profile into the target file, carrying the
// marks its picked chain supplies from the layers below so an inherited secret
// stays a secret, and the ones the user typed, which outrank them.
func commitAdd(cfgPath, name string, extends config.Extends, entries []ui.EnvEntry, under []config.Layer, target config.Config, setDefault bool) error {
	profile, comments := buildProfile(extends, entries)
	profile.Comments = comments
	profile.Marked = mergeMarks(addMarks(under, target, name, extends, entries), profile.Marked)
	return config.UpsertProfile(cfgPath, name, profile, setDefault, true)
}

// addPlacement decides what adding name into the target file means for the layers
// around it. A name the target already declares is the profile's own copy. A name
// it does not declare is either a new profile, or a patch: remote content
// providing the profile is the bootstrap case — the copy overrides that content
// per key while the remote's own copy stays where a re-pull expects it — and the
// patchRemote answer is the record it patches. A name the other user layer
// declares is neither: writing it here would shadow the user's own profile
// rather than patch anything, so the scope hint sends the user to the layer that
// owns it.
func addPlacement(merged, target config.Config, path, name string) (patchRemote string, err error) {
	if _, ok := target.Profiles[name]; ok {
		return "", nil
	}
	layer, ok := merged.ProfileLayer[name]
	if !ok {
		return "", nil
	}
	if remote, isRemote := config.RemoteName(layer); isRemote {
		return remote, nil
	}
	return "", notFoundInTarget(name, path, "add")
}

// addSummary renders add's closing report: where the profile landed, the remote
// whose content it patches, whether it became the default, and how to run it.
func addSummary(name, cfgPath, patchRemote string, setDefault bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n✓ wrote profile %q to %s\n", name, cfgPath)
	if patchRemote != "" {
		fmt.Fprintf(&b, "→ patches remote %q: its content stays the base, these keys override it\n", patchRemote)
	}
	if setDefault {
		b.WriteString("✓ set as default\n")
	}
	fmt.Fprintf(&b, "\nUse it: %s -- <command>\n", config.HintCommand("x", name))
	return b.String()
}

// ciphertextClobberError refuses an add that would write a typed value over one
// only a key can read: without a key the file cannot reproduce that secret, so
// the typed value would destroy it. With a key available the overwrite proceeds —
// the user typed the value on purpose. It reads the file as it stands at the
// gate, not as the command found it, since the collection can run for minutes.
func ciphertextClobberError(cfgPath, name string, entries []ui.EnvEntry) error {
	if app.KeyAvailable(appOpts()) {
		return nil
	}
	prof, _, ok, err := config.ReadProfile(cfgPath, name)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	var keys []string
	for _, e := range entries {
		key, _ := envname.SplitMarked(e.Key)
		if v, exists := envname.Get(prof.Env, key); exists && crypto.IsEncrypted(v) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return fmt.Errorf("refusing to overwrite the encrypted value of %s: no key is available (`%s`, or set --key/ENVER_KEY)",
		strings.Join(keys, ", "), config.HintCommand("decrypt"))
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
	Use:               "add [name]",
	Short:             "Interactively add a profile",
	Args:              cobra.MaximumNArgs(1),
	ValidArgsFunction: completeProfileInTargetOrRemote,
	RunE:              doAdd,
}

func doAdd(cmd *cobra.Command, args []string) error {
	if err := interactiveOnly("add"); err != nil {
		return err
	}
	cfgPath, targetCfg, err := loadTarget()
	if err != nil {
		return err
	}
	// Resolution spans every layer, so the merged view answers the extends
	// judgments and the marks alike; the picker narrows to the global file under
	// --global, where a local parent is not the user's to compose with.
	merged, err := app.Load(appOpts())
	if err != nil {
		return err
	}
	pickerCfg, err := pickerConfig(merged)
	if err != nil {
		return err
	}
	under, err := underLayers(merged)
	if err != nil {
		return err
	}
	names := pickerCfg.ProfileNames()

	name := ""
	if len(args) > 0 {
		name = args[0]
	}
	if name == "" {
		n, ok := promptProfileName("Profile name", ui.Input)
		if !ok {
			return nil
		}
		name = n
	} else if err := validateProfileName(name); err != nil {
		return err
	}
	patchRemote, err := addPlacement(merged, targetCfg, cfgPath, name)
	if err != nil {
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
	if err := extendsCycleError(merged, name, extends); err != nil {
		return err
	}

	var parentEnv map[string]string
	var parentMarked map[string]bool
	var inheritedWarns []string
	if patchRemote != "" {
		// The bootstrap case: the name has no copy here, so its base is the
		// remote content itself rather than a picked extends chain.
		parentEnv, parentMarked, inheritedWarns = remoteBackdrop(merged, name)
	} else {
		parentEnv, parentMarked, inheritedWarns = parentEnvFor(merged, name, extends)
	}
	for _, w := range inheritedWarns {
		fmt.Println("  " + w)
	}

	var entries []ui.EnvEntry
	for {
		entry, err := ui.EnvCardCollecting(ui.EnvEntry{},
			buildSummary(entries, parentEnv, parentMarked, addMarks(under, targetCfg, name, extends, entries)))
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
	if err := ciphertextClobberError(cfgPath, name, entries); err != nil {
		return err
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

	if err := commitAdd(cfgPath, name, extends, entries, under, targetCfg, setDefault); err != nil {
		return err
	}
	fmt.Print(addSummary(name, cfgPath, patchRemote, setDefault))
	return nil
}
