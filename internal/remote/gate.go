package remote

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/neiromaster/enver/internal/config"
	"github.com/neiromaster/enver/internal/crypto"
	"gopkg.in/yaml.v3"
)

const (
	// maxAnchors caps the anchors and aliases one fetched file may carry. They
	// are a data-expansion feature, and the cap bounds the work before the
	// decoder expands anything a fetched file spells out.
	maxAnchors = 1000
	// maxEncryptedValues caps the enc: values one fetched file may carry: a
	// config distributing hundreds of encrypted values is not a distribution
	// artifact, and every one of them is a key the consumer must patch.
	maxEncryptedValues = 100
)

// GateOpts describes the pull fetched content is being validated for.
type GateOpts struct {
	// Record names the remote in the gate messages; empty is allowed and reads
	// as "remote content".
	Record string
	// NoPrefix mirrors the pull mode. A bare-name pull lands in the user's own
	// namespace, so only it raises the collision warning.
	NoPrefix bool
	// Merged is the view the remote's profiles are about to join. The gate reads
	// only its profile names, to detect the collisions a no-prefix pull would
	// cause.
	Merged config.Config
}

// Gate validates fetched remote content before a pull stores it, and returns
// the advisories that do not refuse it. Content is an attacker string: the gate
// is the last line between what an upstream serves and the user's machine, so a
// file it refuses must never reach disk, while a file it accepts must resolve
// under the load path without further complaint.
//
// It refuses content that is not YAML, that carries more than maxAnchors
// anchors and aliases, that the config parser or the load-time name rule
// rejects, whose profile names break the bare upstream rule (no slash, non-empty,
// no control characters), whose extends targets reach outside the file's own
// namespace, that carries an extends cycle within the file, whose ciphertext is
// foreign-era or mixes eras, whose enc:v3 parameters sit beyond the parse caps,
// or that carries more than maxEncryptedValues enc: values.
//
// It admits with a warning content that declares its own remotes: section (which
// projection drops), a no-prefix pull whose bare names collide with profiles the
// merged view already defines, and current-era ciphertext the gate cannot
// decrypt - including the mixed-salt guidance a consumer patching those keys
// needs.
//
// Extends cycles that span files are out of scope here: the gate resolves only
// the file's own graph, and the load backstop catches the rest.
func Gate(content []byte, opts GateOpts) (warnings []string, err error) {
	root, cfg, err := parseRemote(content, opts.Record)
	if err != nil {
		return nil, err
	}
	if err := checkRemoteProfiles(cfg, opts); err != nil {
		return nil, err
	}
	cryptoWarnings, err := checkRemoteCiphertext(cfg, opts.Record)
	if err != nil {
		return nil, err
	}
	return append(gateWarnings(root, cfg, opts), cryptoWarnings...), nil
}

// parseRemote builds both views the checks need: the node tree, so the anchor
// and alias count is taken before the decoder expands a single alias, and the
// decoded config, which carries the load-time parse and name validation.
func parseRemote(content []byte, record string) (*yaml.Node, config.Config, error) {
	label := gateLabel(record)
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return nil, config.Config{}, fmt.Errorf("%s: not valid YAML: %w", label, err)
	}
	if err := checkAnchors(&root); err != nil {
		return nil, config.Config{}, fmt.Errorf("%s: %w", label, err)
	}
	var cfg config.Config
	if root.Kind != 0 {
		if err := root.Decode(&cfg); err != nil {
			return nil, config.Config{}, fmt.Errorf("%s: not a valid config: %w", label, err)
		}
	}
	if err := config.CheckEnvNames(cfg); err != nil {
		return nil, config.Config{}, fmt.Errorf("%s: %w", label, err)
	}
	return &root, cfg, nil
}

// checkAnchors walks the parsed tree counting anchors and aliases. Alias nodes
// are counted but not followed: every alias names an anchor defined in the same
// document, so the tree already holds the target once.
func checkAnchors(n *yaml.Node) error {
	count := 0
	var walk func(*yaml.Node) error
	walk = func(node *yaml.Node) error {
		if node == nil {
			return nil
		}
		if node.Kind == yaml.AliasNode || node.Anchor != "" {
			count++
			if count > maxAnchors {
				return fmt.Errorf("carries more than %d anchors and aliases; a config is not a data-expansion format", maxAnchors)
			}
		}
		for _, child := range node.Content {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(n)
}

// checkRemoteProfiles validates the names an upstream authored, which the load
// path never had to defend against because only the user wrote them before.
// Profile names must be bare: the record prefix supplies the namespace, so a
// slash here would forge a different record's namespace. Extends targets must
// stay inside the file's own namespace: a file resolves its own profiles and the
// records its own remotes: section declares, and nothing else — anything it
// named beyond that would bind whatever the pulling user happens to have.
// Cycles are searched only among the file's own profiles.
func checkRemoteProfiles(cfg config.Config, opts GateOpts) error {
	label := gateLabel(opts.Record)
	defined := make(map[string]bool, len(cfg.Profiles))
	for name := range cfg.Profiles {
		defined[name] = true
	}
	for _, name := range cfg.ProfileNames() {
		if !config.ValidRecordName(name) {
			return fmt.Errorf("%s: profile %s is not a bare upstream profile name (want [A-Za-z0-9][A-Za-z0-9_-]*; the record prefix supplies the namespace)", label, quoteName(name))
		}
		for _, target := range cfg.Profiles[name].Extends {
			if !config.InNamespace(target, defined, cfg.Remotes) {
				return fmt.Errorf("%s: profile %s extends %s, which is outside this file: a remote file resolves only its own profiles and the records in its own remotes: section; declare the base it needs there", label, quoteName(name), quoteName(target))
			}
		}
	}
	if cycle := extendsCycle(cfg, defined); cycle != nil {
		return fmt.Errorf("%s: %w within the file: %s", label, config.ErrExtendsCycle, strings.Join(cycle, " -> "))
	}
	return nil
}

// extendsCycle returns the first cycle among the file's own extends edges as a
// closed path, or nil. A bare target naming one of the file's own profiles is
// the only in-file edge: a target under a declared record names another file's
// graph, and every other shape the gate has already refused.
func extendsCycle(cfg config.Config, defined map[string]bool) []string {
	visiting := map[string]bool{}
	done := map[string]bool{}
	var path []string
	var visit func(string) []string
	visit = func(name string) []string {
		visiting[name] = true
		path = append(path, name)
		defer func() {
			path = path[:len(path)-1]
			delete(visiting, name)
		}()
		for _, target := range cfg.Profiles[name].Extends {
			edge, ok := inFileEdge(target, defined)
			if !ok {
				continue
			}
			if visiting[edge] {
				return append(slices.Clone(path[slices.Index(path, edge):]), edge)
			}
			if done[edge] {
				continue
			}
			if cycle := visit(edge); cycle != nil {
				return cycle
			}
		}
		done[name] = true
		return nil
	}
	for _, name := range cfg.ProfileNames() {
		if done[name] {
			continue
		}
		if cycle := visit(name); cycle != nil {
			return cycle
		}
	}
	return nil
}

// inFileEdge resolves an extends target to the profile of the fetched file it
// names once projection has run, and reports whether it names one at all. A bare
// target naming a defined profile is the only in-file edge: the file may not
// spell its own profiles under the pulled record's name, so no prefixed target
// denotes one of its own.
func inFileEdge(target string, defined map[string]bool) (string, bool) {
	if defined[target] {
		return target, true
	}
	return "", false
}

// encRef is one env value the gate inspects at rest, labelled the way the
// message should name it: profile and env key, both already validated.
type encRef struct {
	profile string
	key     string
	value   string
}

func (r encRef) String() string { return r.profile + "/" + r.key }

// checkRemoteCiphertext era- and format-tests every enc: value without ever
// key-testing one. A foreign-era or mixed-era file can never resolve under this
// build, so landing it only guarantees a broken resolve later; parameters beyond
// the parse caps are the attacker-chosen-cost control, and the per-file count
// cap keeps a fetched file from turning into a key-patching chore. Current-era
// values are admitted: they are the own-config distribution case, and the
// warning tells a consumer who lacks the author's key how to patch them.
func checkRemoteCiphertext(cfg config.Config, record string) (warnings []string, err error) {
	label := gateLabel(record)
	var foreign, current []encRef
	for _, name := range cfg.ProfileNames() {
		profile := cfg.Profiles[name]
		for _, key := range slices.Sorted(maps.Keys(profile.Env)) {
			value := profile.Env[key]
			ref := encRef{profile: name, key: key, value: value}
			switch {
			case crypto.ForeignEncPrefix(value) != "":
				foreign = append(foreign, ref)
			case crypto.IsEncrypted(value):
				current = append(current, ref)
			}
		}
	}
	if len(foreign) > 0 {
		return nil, foreignEraError(label, foreign, len(current) > 0)
	}
	if len(current) > maxEncryptedValues {
		return nil, fmt.Errorf("%s: carries %d encrypted values; a remote config may carry at most %d", label, len(current), maxEncryptedValues)
	}
	var over []encRef
	var overParams crypto.Argon2Params
	for _, ref := range current {
		_, params, err := crypto.SaltFromValue(ref.value)
		if err != nil {
			return nil, fmt.Errorf("%s: %s: malformed encrypted value: %w", label, ref, err)
		}
		if exceedsCurrentParams(params) {
			if len(over) == 0 {
				overParams = params
			}
			over = append(over, ref)
		}
	}
	if len(over) > 0 {
		warnings = append(warnings, fmt.Sprintf("%s: %s encrypts with KDF parameters above this build's defaults (e.g. argon2id t=%d m=%d p=%d); they parse and decrypt within the caps, but a higher t or m makes every resolve cost more than a current-era value", label, joinRefs(over, false), overParams.Time, overParams.Memory, overParams.Threads))
	}
	if len(current) > 0 {
		warnings = append(warnings, encryptedWarning(label, current))
	}
	return warnings, nil
}

// foreignEraError describes ciphertext this build cannot read. A file that holds
// both foreign-era and current-era values is reported as mixed, since that is
// the more specific breakage: no single era rule can serve it. Every distinct
// foreign prefix is named, so a file spanning more than one past era says so.
func foreignEraError(label string, foreign []encRef, mixed bool) error {
	prefixes := foreignPrefixes(foreign)
	if mixed {
		return fmt.Errorf("%s: mixes ciphertext eras: %s carries %s beside current-era enc:v3 values; re-encrypt every value with this enver build", label, joinRefs(foreign, false), prefixes)
	}
	return fmt.Errorf("%s: %s carries %s, which this enver build cannot read; remote content must carry current-era enc:v3 values", label, joinRefs(foreign, false), prefixes)
}

// foreignPrefixes renders the distinct enc-family prefixes found in sorted
// order.
func foreignPrefixes(foreign []encRef) string {
	seen := map[string]struct{}{}
	var prefixes []string
	for _, ref := range foreign {
		prefix := crypto.ForeignEncPrefix(ref.value)
		if _, ok := seen[prefix]; ok {
			continue
		}
		seen[prefix] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	slices.Sort(prefixes)
	return strings.Join(prefixes, ", ")
}

// encryptedWarning reports the current-era values the gate cannot decrypt and
// carries the supported patch path from the spec: plaintext patches while any
// author ciphertext remains, encryption only once every foreign value is
// replaced, because two salts in one profile break passphrase recovery.
func encryptedWarning(label string, current []encRef) string {
	return fmt.Sprintf("%s: %d encrypted value(s) decrypt only for the author's key: %s; patch these keys with plaintext values while foreign ciphertext remains, and encrypt only after every foreign value is replaced - your ciphertext beside the author's puts two salts in one profile and breaks passphrase recovery", label, len(current), joinRefs(current, true))
}

// exceedsCurrentParams reports whether p costs more than this build's own
// parameters. Such values parse within the caps and resolve correctly; the
// warning exists because each resolve costs more than the default.
func exceedsCurrentParams(p crypto.Argon2Params) bool {
	current := crypto.CurrentParams
	return p.Time > current.Time || p.Memory > current.Memory || p.Threads > current.Threads
}

// joinRefs renders refs as profile/key, all of them or a bounded sample.
func joinRefs(refs []encRef, all bool) string {
	const maxShown = 3
	shown := refs
	suffix := ""
	if !all && len(shown) > maxShown {
		suffix = fmt.Sprintf(" and %d more", len(shown)-maxShown)
		shown = shown[:maxShown]
	}
	parts := make([]string, len(shown))
	for i, ref := range shown {
		parts[i] = ref.String()
	}
	return strings.Join(parts, ", ") + suffix
}

// gateWarnings collects the advisories that admit the content: a section a
// remote must not own, and the namespace collisions a bare-name pull would
// cause.
func gateWarnings(root *yaml.Node, cfg config.Config, opts GateOpts) []string {
	var warnings []string
	if topLevelHasKey(root, "remotes") {
		warnings = append(warnings, fmt.Sprintf("%s: declares a remotes: section; a remote cannot register fetches, so the section is ignored", gateLabel(opts.Record)))
	}
	if collision := collisionWarning(cfg, opts); collision != "" {
		warnings = append(warnings, collision)
	}
	return warnings
}

// topLevelHasKey reports whether the document's top-level mapping declares key.
// It answers presence, not content: an empty remotes: section still tells the
// user the author expected nested remotes to work.
func topLevelHasKey(root *yaml.Node, key string) bool {
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value == key {
			return true
		}
	}
	return false
}

// collisionWarning reports every bare name a no-prefix pull would land on top of
// an existing merged profile. The check runs on every pull, first or repeat, so
// re-pulling a bare-name record never silently rewrites the namespace it shares.
func collisionWarning(cfg config.Config, opts GateOpts) string {
	if !opts.NoPrefix {
		return ""
	}
	var colliding []string
	for _, name := range cfg.ProfileNames() {
		if _, ok := opts.Merged.Profiles[name]; ok {
			colliding = append(colliding, quoteName(name))
		}
	}
	if len(colliding) == 0 {
		return ""
	}
	return fmt.Sprintf("%s: stored without a prefix, so its profiles keep their bare names; these existing merged profiles will be overridden: %s", gateLabel(opts.Record), strings.Join(colliding, ", "))
}

// gateLabel names the remote in a gate message.
func gateLabel(record string) string {
	if record == "" {
		return "remote content"
	}
	return fmt.Sprintf("remote %q", record)
}

// quoteName renders an attacker-authored name for a message: quoted, so control
// characters cannot reach the terminal raw, and bounded, so a long name cannot
// flood the line it lands on. The bound falls back to a rune boundary, so a
// truncated name never renders its split rune as escaped invalid bytes.
func quoteName(name string) string {
	const maxLen = 48
	if len(name) > maxLen {
		cut := maxLen
		for cut > 0 && !utf8.RuneStart(name[cut]) {
			cut--
		}
		name = name[:cut] + "..."
	}
	return fmt.Sprintf("%q", name)
}
