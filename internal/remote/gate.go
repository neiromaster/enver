package remote

import (
	"errors"
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
	// maxAnchors caps the anchors and aliases one checked file may carry. They
	// are a data-expansion feature, and the cap bounds the work before the
	// decoder expands anything the file spells out.
	maxAnchors = 1000
	// maxEncryptedValues caps the enc: values one checked file may carry: a
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
// namespace or carry a segment no profile can be named by, that carries an
// extends cycle within the file, whose nested declarations a pull could not
// act on (no URL, an unresolvable source, a name no filesystem can carry, a
// case-fold sibling), whose ciphertext is
// foreign-era or mixes eras, whose enc:v3 parameters sit beyond the parse caps,
// or that carries more than maxEncryptedValues enc: values.
//
// It admits with a warning a no-prefix pull whose bare names collide with
// profiles the merged view already defines, and a file that declares a record
// named like one of its own profiles, which coexists with it but seldom means
// to. It admits in silence content that declares its own remotes: section,
// which nested composition loads from. It also admits current-era ciphertext
// the gate cannot decrypt - including the mixed-salt guidance a consumer
// patching those keys needs.
//
// Extends cycles that span files are out of scope here: the gate resolves only
// the file's own graph, and the load backstop catches the rest.
//
// Gate is Check over the publish rules with the pull in scope: it refuses on
// the first error finding and returns no warnings then, and joins the collision
// advisory, which only a pull can judge, in front of the findings' warnings
// and notes, all returned as warnings.
func Gate(content []byte, opts GateOpts) (warnings []string, err error) {
	cfg, findings, err := Check(content, Publish, gateLabel(opts.Record))
	if err != nil {
		return nil, err
	}
	if len(findings) != 0 && findings[0].Severity == config.SeverityError {
		return nil, findings[0].errOf()
	}
	var out []string
	if collision := collisionWarning(cfg, opts); collision != "" {
		out = append(out, collision)
	}
	for _, finding := range findings {
		out = append(out, finding.Text)
	}
	return out, nil
}

// Mode is the rule set a checked file is judged by.
type Mode int

const (
	// Publish applies the rules a pull applies to fetched content: profile
	// names are bare, and extends stays inside the file's own namespace.
	Publish Mode = iota
	// Local applies the rules a file's own content declares: a profile may be
	// named llm/ollama, and extends is judged against what the file declares.
	Local
)

// Finding is one refusal or advisory a check of a file produced. Severity
// grades it. Profile and Target name the profile and the extends target the
// finding is about, when it is about one. Text is the message, already
// carrying the label prefix the caller passed to Check.
type Finding struct {
	Severity config.Severity
	Profile  string
	Target   string
	Text     string

	// cause is the constructed error an error finding was rendered from, kept
	// so Gate can return an error a caller matches with errors.Is. Findings a
	// caller builds have no cause.
	cause error
}

// Check validates one enver document by the rules its mode declares and
// reports every finding instead of the first refusal. Publish applies the
// rules a pull applies to fetched content; Local applies the rules an
// unmarked file's own content declares, where a profile may be named
// llm/ollama. Label prefixes every finding's text as label + ": " when
// non-empty. Findings are ordered errors first, then warnings, then notes;
// within a severity, in the order the checks run. Err is reserved for
// failures that are not about the content: every content problem is a
// finding, so a caller sees all of them at once.
func Check(content []byte, mode Mode, label string) (cfg config.Config, findings []Finding, err error) {
	prefix := ""
	if label != "" {
		prefix = label + ": "
	}
	cfg, parsed, ended := parseStage(content, prefix)
	if ended {
		return config.Config{}, parsed, nil
	}
	findings = parsed
	if mode == Local {
		findings = append(findings, checkLocalExtends(cfg, prefix)...)
	} else {
		findings = append(findings, checkRemoteProfiles(cfg, prefix)...)
	}
	findings = append(findings, checkNestedDeclarations(cfg, prefix)...)
	findings = append(findings, checkRemoteCiphertext(cfg, prefix)...)
	findings = append(findings, childNameFindings(cfg, prefix)...)
	slices.SortStableFunc(findings, func(a, b Finding) int {
		return severityRank(a.Severity) - severityRank(b.Severity)
	})
	return cfg, findings, nil
}

// parseStage decodes the file the way the checks read it: the node tree first,
// so the anchor and alias count is taken before the decoder expands a single
// alias, then the config view, which carries the load-time parse. A document
// that is not YAML, is not a config, or exceeds the anchor cap yields no
// config and ends the check with its one finding; names the load-time rule
// refuses are findings the later stages still run past.
func parseStage(content []byte, prefix string) (config.Config, []Finding, bool) {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return config.Config{}, []Finding{newErrorFinding(prefix, "", "", fmt.Errorf("not valid YAML: %w", err))}, true
	}
	if err := checkAnchors(&root); err != nil {
		return config.Config{}, []Finding{newErrorFinding(prefix, "", "", err)}, true
	}
	var cfg config.Config
	if root.Kind != 0 {
		if err := root.Decode(&cfg); err != nil {
			return config.Config{}, []Finding{newErrorFinding(prefix, "", "", fmt.Errorf("not a valid config: %w", err))}, true
		}
	}
	return cfg, checkEnvNames(cfg, prefix), false
}

// checkEnvNames reports every name the load-time rule refuses, one finding
// each. The rule is stated once in config and stops at its first refusal, so
// each candidate is checked alone: the texts stay the load path's own, and a
// file with several bad names reports all of them.
func checkEnvNames(cfg config.Config, prefix string) []Finding {
	var findings []Finding
	for _, name := range slices.Sorted(maps.Keys(cfg.Remotes)) {
		one := config.Config{Remotes: map[string]config.RemoteRecord{name: cfg.Remotes[name]}}
		if err := config.CheckEnvNames(one); err != nil {
			findings = append(findings, newErrorFinding(prefix, "", name, err))
		}
	}
	for _, name := range cfg.ProfileNames() {
		one := config.Config{Profiles: map[string]config.Profile{name: cfg.Profiles[name]}}
		if err := config.CheckEnvNames(one); err != nil {
			findings = append(findings, newErrorFinding(prefix, name, "", err))
		}
	}
	return findings
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
// named beyond that would bind whatever the pulling user happens to have. An
// in-namespace target must also be well-formed, because the projection
// classifies control-stripped targets and a segment it would silently clean has
// to be refused here instead: what the gate accepts must resolve as stored.
// Cycles are searched only among the file's own profiles.
func checkRemoteProfiles(cfg config.Config, prefix string) []Finding {
	defined := make(map[string]bool, len(cfg.Profiles))
	for name := range cfg.Profiles {
		defined[name] = true
	}
	var findings []Finding
	for _, name := range cfg.ProfileNames() {
		if !config.ValidRecordName(name) {
			findings = append(findings, newErrorFinding(prefix, name, "", fmt.Errorf("profile %s is not a bare upstream profile name (want [A-Za-z0-9][A-Za-z0-9_-]*; the record prefix supplies the namespace)", quoteName(name))))
		}
		for _, target := range cfg.Profiles[name].Extends {
			if !config.InNamespace(target, defined, cfg.Remotes) {
				findings = append(findings, newErrorFinding(prefix, name, target, fmt.Errorf("profile %s extends %s, which is outside this file: a remote file resolves only its own profiles and the records in its own remotes: section; declare the base it needs there", quoteName(name), quoteName(target))))
				continue
			}
			if bad, has := invalidTargetSegment(target); has {
				findings = append(findings, newErrorFinding(prefix, name, target, fmt.Errorf("profile %s extends %s, whose %s segment is not a valid profile name (want [A-Za-z0-9][A-Za-z0-9_-]*)", quoteName(name), quoteName(target), quoteName(bad))))
			}
		}
	}
	if cycle := extendsCycle(cfg, defined); cycle != nil {
		findings = append(findings, cycleFinding(prefix, cycle))
	}
	return findings
}

// checkLocalExtends applies the local extends table to the file's own
// declarations. File mode reads no pulled content, so a target is judged
// against what the file declares: its own profiles and its own remotes:
// section. A target whose spelling names one of the file's own profiles is
// fine, bare or slash-named — the local view resolves it by name — and so is a
// target under a declared prefixed record, since whether that record's content
// carries the profile is the pull's check. A declared no-prefix record softens
// the bare case: its profiles land bare, so an undeclared bare name may be one
// of them, and a prefixed spelling is refused for the bare name it hides. A
// target is segment-checked only when it drew no other finding, the way the
// publish rules judge a target's shape before its spelling.
func checkLocalExtends(cfg config.Config, prefix string) []Finding {
	defined := make(map[string]bool, len(cfg.Profiles))
	for name := range cfg.Profiles {
		defined[name] = true
	}
	var noPrefix []string
	for _, name := range slices.Sorted(maps.Keys(cfg.Remotes)) {
		if cfg.Remotes[name].NoPrefix {
			noPrefix = append(noPrefix, quoteName(name))
		}
	}
	var findings []Finding
	for _, name := range cfg.ProfileNames() {
		for _, target := range cfg.Profiles[name].Extends {
			verdict, drew := localTargetVerdict(cfg, defined, noPrefix, name, target, prefix)
			if drew {
				findings = append(findings, verdict)
				continue
			}
			if bad, has := invalidTargetSegment(target); has {
				findings = append(findings, newErrorFinding(prefix, name, target, fmt.Errorf("profile %s extends %s, whose %s segment is not a valid profile name (want [A-Za-z0-9][A-Za-z0-9_-]*)", quoteName(name), quoteName(target), quoteName(bad))))
			}
		}
	}
	if cycle := extendsCycle(cfg, defined); cycle != nil {
		findings = append(findings, cycleFinding(prefix, cycle))
	}
	return findings
}

// localTargetVerdict judges one extends target against what the file declares.
// It reports the finding, if any, and whether it drew one. A target whose
// spelling names one of the file's own profiles is fine whether it is bare or
// slash-named: the local view resolves it by name, so the record rows below
// apply only to targets that are not own profiles. A bare name nothing in the
// file can supply is an error unless a declared no-prefix record could be
// holding it, which is only a note, and a prefixed target under such a record
// is refused for the bare name it hides.
func localTargetVerdict(cfg config.Config, defined map[string]bool, noPrefix []string, name, target, prefix string) (Finding, bool) {
	if defined[target] {
		return Finding{}, false
	}
	head, rest, hasSlash := strings.Cut(target, "/")
	if !hasSlash {
		if len(noPrefix) > 0 {
			noun := "record"
			if len(noPrefix) > 1 {
				noun = "records"
			}
			return newFinding(config.SeverityNote, prefix, name, target, fmt.Sprintf("profile %s extends %s, which may name a profile of the no-prefix %s %s: such a record's profiles land bare, and file mode reads no pulled content, so the target is not checked", quoteName(name), quoteName(target), noun, strings.Join(noPrefix, ", "))), true
		}
		return newErrorFinding(prefix, name, target, fmt.Errorf("profile %s extends %s, which no profile of this file declares: the target resolves to nothing; define the base as a profile of this file", quoteName(name), quoteName(target))), true
	}
	rec, declared := cfg.Remotes[head]
	switch {
	case !declared:
		return newErrorFinding(prefix, name, target, fmt.Errorf("profile %s extends %s, but this file declares no record %s: a local file resolves only the records in its own remotes: section", quoteName(name), quoteName(target), quoteName(head))), true
	case rec.NoPrefix:
		return newErrorFinding(prefix, name, target, fmt.Errorf("profile %s extends %s, but record %s is declared with no_prefix: in the --file view its profiles land bare, so spell the bare name %s", quoteName(name), quoteName(target), quoteName(head), quoteName(rest))), true
	default:
		return Finding{}, false
	}
}

// cycleFinding renders an in-file extends cycle: the chain is the finding's
// target, and the error keeps config.ErrExtendsCycle as its cause, so a caller
// can tell the cycle apart from every other refusal with errors.Is.
func cycleFinding(prefix string, cycle []string) Finding {
	chain := strings.Join(cycle, " -> ")
	return newErrorFinding(prefix, "", chain, fmt.Errorf("%w within the file: %s", config.ErrExtendsCycle, chain))
}

// invalidTargetSegment returns the first /-separated segment of target that no
// profile can be named by, and whether it found one. The flag is the answer, not
// the segment: an empty segment is itself a fault, so the fault cannot be spelled
// by the sentinel. The check belongs to the gate, not the projection: the
// projection strips control characters before it classifies, so only here can a
// target that would classify differently after cleaning be caught before it
// lands on disk.
func invalidTargetSegment(target string) (string, bool) {
	for _, segment := range strings.Split(target, "/") {
		if !config.ValidRecordName(segment) {
			return segment, true
		}
	}
	return "", false
}

// extendsCycle returns the first cycle among the file's own extends edges as a
// closed path, or nil. An edge is any target whose exact spelling names one of
// the file's own profiles, bare or slash-named: Local lets a profile carry the
// slash a published file may not. A target under a declared record names
// another file's graph and is never an edge.
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

// inFileEdge resolves an extends target to the profile of the checked file it
// names, and reports whether it names one at all: the target's exact spelling
// must be one of the file's own profile names. A slash-named profile is
// therefore reachable only where such names are legal, and a prefixed target
// names one only when the file itself carries slash-named profiles.
func inFileEdge(target string, defined map[string]bool) (string, bool) {
	if defined[target] {
		return target, true
	}
	return "", false
}

// checkNestedDeclarations applies the record usability rule to the records a
// checked file declares: a pull is about to fetch each of them and store
// content under its name, so a declaration no pull could act on — a missing or
// unresolvable source, a name no filesystem can carry, two names the
// filesystem may not tell apart — must refuse the file now rather than fail
// the walk after the parent has landed. One rule serves both authors: this is
// the same validation a user's own remotes: section passes on write.
func checkNestedDeclarations(cfg config.Config, prefix string) []Finding {
	if len(cfg.Remotes) == 0 {
		return nil
	}
	if err := config.ValidateRemotes(cfg.Remotes); err != nil {
		return []Finding{newErrorFinding(prefix, "", "", fmt.Errorf("declares remotes a pull cannot use: %w", err))}
	}
	return nil
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
// values are admitted: they are the own-config distribution case, and the note
// tells a consumer who lacks the author's key how to patch them.
func checkRemoteCiphertext(cfg config.Config, prefix string) []Finding {
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
		return []Finding{newErrorFinding(prefix, "", "", foreignEraError(foreign, len(current) > 0))}
	}
	if len(current) > maxEncryptedValues {
		return []Finding{newErrorFinding(prefix, "", "", fmt.Errorf("carries %d encrypted values; a remote config may carry at most %d", len(current), maxEncryptedValues))}
	}
	var over []encRef
	var overParams crypto.Argon2Params
	for _, ref := range current {
		_, params, err := crypto.SaltFromValue(ref.value)
		if err != nil {
			return []Finding{newErrorFinding(prefix, ref.profile, ref.key, fmt.Errorf("%s: malformed encrypted value: %w", ref, err))}
		}
		if exceedsCurrentParams(params) {
			if len(over) == 0 {
				overParams = params
			}
			over = append(over, ref)
		}
	}
	var findings []Finding
	if len(over) > 0 {
		findings = append(findings, newFinding(config.SeverityNote, prefix, "", "", fmt.Sprintf("%s encrypts with KDF parameters above this build's defaults (e.g. argon2id t=%d m=%d p=%d); they parse and decrypt within the caps, but a higher t or m makes every resolve cost more than a current-era value", joinRefs(over, false), overParams.Time, overParams.Memory, overParams.Threads)))
	}
	if len(current) > 0 {
		findings = append(findings, newFinding(config.SeverityNote, prefix, "", "", encryptedWarning(current)))
	}
	return findings
}

// foreignEraError describes ciphertext this build cannot read. A file that holds
// both foreign-era and current-era values is reported as mixed, since that is
// the more specific breakage: no single era rule can serve it. Every distinct
// foreign prefix is named, so a file spanning more than one past era says so.
func foreignEraError(foreign []encRef, mixed bool) error {
	prefixes := foreignPrefixes(foreign)
	if mixed {
		return fmt.Errorf("mixes ciphertext eras: %s carries %s beside current-era enc:v3 values; re-encrypt every value with this enver build", joinRefs(foreign, false), prefixes)
	}
	return fmt.Errorf("%s carries %s, which this enver build cannot read; remote content must carry current-era enc:v3 values", joinRefs(foreign, false), prefixes)
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

// encryptedWarning reports the current-era values the check cannot decrypt and
// carries the supported patch path from the spec: plaintext patches while any
// author ciphertext remains, encryption only once every foreign value is
// replaced, because two salts in one profile break passphrase recovery.
func encryptedWarning(current []encRef) string {
	return fmt.Sprintf("%d encrypted value(s) decrypt only for the author's key: %s; patch these keys with plaintext values while foreign ciphertext remains, and encrypt only after every foreign value is replaced - your ciphertext beside the author's puts two salts in one profile and breaks passphrase recovery", len(current), joinRefs(current, true))
}

// exceedsCurrentParams reports whether p costs more than this build's own
// parameters. Such values parse within the caps and resolve correctly; the
// note exists because each resolve costs more than the default.
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

// childNameFindings reports each declared record whose name equals one of the
// file's own profile names. The two coexist — the record's profiles land under
// <name>/ while the profile keeps the bare name — so the declaration is
// allowed, but a reader can easily have meant one thing where the config now
// holds two, and the check output is where they find out.
func childNameFindings(cfg config.Config, prefix string) []Finding {
	var out []Finding
	for _, name := range slices.Sorted(maps.Keys(cfg.Remotes)) {
		if _, ok := cfg.Profiles[name]; !ok {
			continue
		}
		out = append(out, newFinding(config.SeverityWarning, prefix, name, "", fmt.Sprintf("declares a record %q beside its own profile of the same name; the record's profiles land under %s/ while the profile keeps the bare name, so both load — check that is intended", quoteName(name), quoteName(name))))
	}
	return out
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

// severityRank orders findings in the report: errors speak first, warnings
// follow, notes trail.
func severityRank(s config.Severity) int {
	switch s {
	case config.SeverityError:
		return 0
	case config.SeverityWarning:
		return 1
	case config.SeverityNote:
		return 2
	default:
		return 3
	}
}

// newFinding renders one finding's text under the label prefix.
func newFinding(severity config.Severity, prefix, profile, target, text string) Finding {
	return Finding{Severity: severity, Profile: profile, Target: target, Text: prefix + text}
}

// newErrorFinding keeps the error a finding was rendered from, already
// carrying the label the text carries, so Gate can return an error whose text
// is the finding's and whose cause a caller still matches with errors.Is.
func newErrorFinding(prefix, profile, target string, err error) Finding {
	labeled := fmt.Errorf("%s%w", prefix, err)
	return Finding{Severity: config.SeverityError, Profile: profile, Target: target, Text: labeled.Error(), cause: labeled}
}

// errOf renders an error finding as the error Gate refuses a pull with: the
// constructed error itself when there is one, so its wrapped cause stays
// matchable with errors.Is; the text alone otherwise.
func (f Finding) errOf() error {
	if f.cause != nil {
		return f.cause
	}
	return errors.New(f.Text)
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
