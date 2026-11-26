# Changelog

## Unreleased

- **Breaking:** a key's name no longer makes it a secret. `enver encrypt`
  seals `~`-marked values and values with credentials in a URL; other
  plaintext stays plaintext. `show` masks marked values, values with URL
  credentials, and any value stored as ciphertext. Values sealed by an
  earlier version under an unmarked key stay sealed and masked, but
  `decrypt` returns them unmarked — add `~` before re-encrypting.
- **New:** `enver --file <path>` (short `-f`) works on one config file as the
  whole config. Reads, writes and completion come from that file alone, and
  the content pulled for it is stored in `.enver/<file>/` beside it, laid out
  like the shared store. `--config`, `--no-local`, `-g`/`--global` and
  `pull --local` are errors together with `--file`. `validate` refuses `--file`
  until named-file checks arrive.
- **Breaking:** `keygen` loses the `-f` shorthand, which `--file` takes;
  `--force` stays.

## v0.13.0 - 2026-11-25

- **New:** read commands compose profiles at launch. `enver x base slotA --
  claude` (and the same profile list in `show`, `export`, `dotenv`) resolves
  each member with its own `extends` chain and folds the members left to
  right — an anonymous `extends`: later members win shared keys, sibling
  unsets never hide sibling values (only a member's own chain fences), and
  the config `default` applies only when the list is empty. The composed
  label `base+slotA` names the run in the tab title, JSON `profile` field
  and dotenv header; completion offers profiles at every position before
  `--` — already-typed members excluded — and falls back to the child
  command's file completion after `--`.

## v0.12.0 - 2026-11-24

- **New:** a fetched remote config can declare its own `remotes:` records, and
  one pull brings the whole tree: the provider config and the base it extends
  are fetched, gated and stored together, nested under the record's path
  (`remotes/ollama.yaml` beside a directory `ollama/` holding
  `ollama/claude.yaml`, each record with its own sidecar, commit and pull
  time). Scoping is lexical — a file resolves only its own profiles and the
  records its own `remotes:` block declares — and prefixes compose at
  boundaries without leaking inward, so pulling a provider under another name
  (`enver pull <source> --as my-ollama`) moves the whole subtree wholesale and
  rewrites nothing inside the files. `no_prefix` on a record drops just that
  boundary's segment. A re-pull syncs the subtree to the parent's current
  declaration: newly declared children are fetched, dropped children's stored
  files are deleted as reproducible cache and listed in the output, a child
  whose declaration changed URL or ref is fetched fresh, and an unchanged
  child is kept untouched. `pull --list` renders the tree with nested rows indented
  under their parent, and loading never touches the network: a declared child
  with no stored content contributes no profiles and advises the re-pull. A
  declaration tree that loops back to a source is refused at pull naming the
  chain, and a tree deeper than 16 records is refused loudly rather than
  truncated.
- **Breaking:** the passthrough form of remote `extends` is gone. A remote
  file whose `extends` named a target it did not define used to resolve that
  target against the pulling user's namespace — how a remote extended a
  profile you provide, and how cross-remote extends composed. An
  out-of-namespace target in any remote file, top-level or nested, now refuses
  at pull and errors at load; `enver validate` reports every dangling
  `extends` as an error, with no more authorship grading, and `list --format
  json` carries no `skipped` array. A remote that needs a base now declares it
  in its own `remotes:` and the pull fetches it; user-side composition is
  unchanged — a local profile extends `record/profile` as before, and a local
  profile with the same name as a projected one patches it per key, local keys
  winning while the remote's other keys survive.

## v0.11.0 - 2026-11-17

- **New:** `gist:owner/id[/file][@revision]` remote sources — one file in a
  GitHub Gist, `enver.yaml` by default, pinned by the full 40-character
  revision sha (a gist has no branches, tags or HEAD, so an abbreviated or
  uppercase sha is refused at parse time). An unpinned gist pull resolves the
  revision the gist serves through the gist API before fetching, so the
  sidecar records the revision the stored content came from; a down or
  rate-limited API costs that record, not the pull. An `https://` URL to a
  gist raw file keeps working as an ordinary https source, without revision
  tracking.

## v0.10.0 - 2026-11-16

- **New:** a leading `~` on an env key marks the variable a secret candidate —
  `~PROJECT_PATH: /home/anna/enver` is encrypted by `enver encrypt` and masked
  by `enver show` even though its name matches no secret heuristic. The mark is
  file and display syntax only: `x`, `export`, `dotenv` and `show --format
  json` keep the bare name, the mark survives `encrypt` and `decrypt` so the
  variable stays a candidate, and a closer unmarked definition (a local
  `.enver.yaml` overriding a shared global entry) makes it plaintext for that
  resolution only. Marking never encrypts on its own: a marked value still in
  plaintext is masked and annotated `~ marked, not encrypted`, and `enver
  validate` warns `plaintext-secret`. `add` and `edit` accept `~` in the
  variable-name field, and `edit` gains a **Secrets…** menu that stages marks
  for own and inherited variables, with an **Encrypt now** step that runs after
  the commit.
- `enver edit` decrypts encrypted values when a key resolves and re-encrypts
  them on save under the file's own KDF era, so editing a sealed value no
  longer needs `enver decrypt` first and its plaintext can no longer land in
  the file. Without a key the ciphertext is left untouched and its card stays
  shut; under `ENVER_KEY`, which carries no salt, a profile holding encrypted
  values cannot be saved at all, and Encrypt now saves the marks without
  encrypting and says so.
- Writers no longer drop a mark they were not told about: `import`, `add` and
  `duplicate` keep an existing `~KEY` entry's mark, including a mark only an
  ancestor profile carries, and `edit` seeds the inherited mark into any
  override it materializes. Previously an upsert appended `K` beside `~K` —
  which the loader rejects — or silently declassified the key for the next
  `encrypt`.
- `enver show` in text mode degrades when no key source resolves: an unreadable
  value prints `KEY=(cannot decrypt: no key — run enver keygen)` and the
  command exits 0, while `--format json`, `export`, `dotenv` and `x` still fail
  and name the keys that blocked them. `add` refuses to overwrite an encrypted
  value without a key, and a mutating command now fails on a corrupt other
  layer instead of writing the key unmarked.
- **New:** remote configs — a config layer can declare `remotes:` records that
  fetch a config file from `github:owner/repo[/path][@ref]` or a plain `https`
  URL and fold it into the merged view as the lowest-priority layer, so
  personal or team configs live in a repository and follow you across machines.
  `enver pull` registers, fetches and pins a record, `enver pull <name>`
  re-fetches, `enver pull --rm <name>` removes it, and `enver pull --list`
  shows each record with its URL, ref and last-pulled commit and time. Remote
  profiles surface as `<record>/<profile>` with their same-file `extends`
  rewritten, so they never shadow a user profile; `no_prefix: true` inlines
  them bare with load-time collision warnings.
- Pulling gates content before it lands (YAML sanity, env names, extends
  cycles, KDF parameter caps, foreign ciphertext) and refuses cross-host
  redirects, protocol downgrades and bodies over 8 MiB. Unpinned GitHub
  content is pinned by commit sha, and a re-fetch whose stored content came
  from another source warns and asks before replacing it while your patch
  profiles exist for the prefix. A value the local key cannot read degrades
  per key in `show` (with an `undecryptable` report in JSON), while `x`,
  `export` and `dotenv` refuse and name the keys; your own wrong-key values
  still fail everywhere.
- `enver add <record>/<profile>` bootstraps a patch profile that composes over
  a remote without shadowing it, and writers fold marks over the full
  under-stack, so a remote's `~KEY` reaches a patch of it. `validate` grades a
  remote's dangling `extends` as a warning naming the record while your own
  stay errors, a reference into a declared-but-unpulled remote suggests the
  pull, and `list --format json` reports skipped remote-dangling profiles in a
  `skipped` array.

## v0.9.5 - 2026-11-09

- The Homebrew cask is published from the shared `neiromaster/homebrew-tap`
  repository now instead of the enver-only tap, so the install snippet is
  `brew tap neiromaster/tap` followed by `brew trust neiromaster/tap` and
  `brew install enver`. The old `neiromaster/enver` name still redirects to the
  moved repository.
- Infra: the npm publish is retried across transient CDN errors when it fetches
  the release assets, and it runs as its own release job that can be dispatched
  for an already-published tag — re-running a failed tag build used to redo
  goreleaser, which fails on the assets it already uploaded.

## v0.9.4 - 2026-11-08

- The Homebrew cask is now generated with Homebrew's current
  `postflight_steps` stanza. The deprecated `postflight` one it replaced made
  every `brew` command print a deprecation warning for anyone with the tap
  installed. Clearing the macOS quarantine attribute is also guarded to macOS
  now — the cask serves Linux too, where `/usr/bin/xattr` does not exist.

## v0.9.3 - 2026-11-08

- **Security:** `ENVER_KEY` no longer reaches the child environment. `enver x`
  used to copy `os.Environ()` wholesale, so anything it ran — the documented
  CI pattern hands the key over as an env var — could read the master key and
  decrypt every profile. Nested enver workflows (make → `enver show`) that
  need it back can set `ENVER_FORWARD_KEY=1` to re-export the resolved key to
  the child; a set key that is dropped says so on stderr.
- **Security:** decrypt persists the plaintext config `0600` instead of the
  umask default, the key cache is re-tightened to `0600` when overwriting an
  existing looser file, and a relative `XDG_CONFIG_HOME` is ignored per the
  XDG spec instead of silently writing the key into the cwd.
- `enver x` resolves the child command against the merged environment's
  `PATH`, so a profile that sets `PATH:` works with a bare command name
  instead of exiting 127; lookup failures other than not-found surface
  verbatim, and in the wait path (coverage builds, Windows) a child killed by
  a signal exits `128+signal` instead of 255.
- `enver x prod ls` without `--` now echoes the interpreted split — the token
  taken as the profile and the text dropped — instead of only demanding a
  command after `--`.
- Nested references inside braced operands expand correctly
  (`${A:-${B}}`): the closer is matched by brace depth, so a set default no
  longer leaves a stray `}` in the value and an unset one no longer ships the
  inner reference literally.
- Password prompts: `Ctrl-C` cancels cleanly into the normal prompt loop (the
  terminal is no longer left with echo off), and line-discipline control
  bytes (`^W`, `^R`, `^V`, `^S`, `^Q`, `^O`) are consumed faithfully instead
  of being appended to the passphrase.
- Prompts render on stderr, matching `Password:` — `enver rename p > log`
  captures neither TUI frames nor raw-mode escape sequences, only the
  command's own output.
- `enver edit` survives its error paths: a commit-time validation failure
  (an extends cycle) keeps the session open with the message instead of
  exiting and losing unsaved edits — cycles are also bounced at pick time
  like in `add` — a real menu failure exits non-zero instead of silently
  succeeding, and `*` under an active filter clears only the visible ranks
  instead of the whole `extends` chain.
- `enver rename` and `duplicate` re-prompt on an invalid prompted name like
  `add` does, instead of hard-erroring the whole command.
- "No default profile set" guards now carry the next-step hint every sibling
  guard has (`enver default <profile>`).
- **New:** `enver import --extends` completes profile parents for the
  trailing comma-segment, skipping parents already picked; `--format` on
  `show`, `list`, and `export` completes its enum without falling through to
  filename completion.
- YAML configs whose env keys spell booleans (`TRUE:`, `False:`, `on:` …)
  load through the struct path but were invisible to the node-tree writers —
  an upsert appended a duplicate mapping key that bricked every subsequent
  read, and delete silently no-oped. Writers now match any scalar key tag.
- `enver update` refuses to run from a from-source build (version `v0.0.0`)
  instead of always reporting an update and replacing the local binary with
  the latest release.
- `scripts/install.sh` extracts into a work dir and swaps the binary in by
  rename, so an interrupted install can no longer leave a truncated `enver`
  clobbering a working one, and reinstalling over a running binary avoids
  ETXTBSY.
- Infra: the CI test matrix runs on macOS too; publishing a release requires
  a green test run; `go mod tidy` is verified to leave no diff in both
  modules; every GitHub Actions ref is pinned to a commit SHA; runs group by
  ref with cancel-in-progress on CI; the generated shell completions are
  committed with a drift check; `docs/contributing.md` matches the CI gates
  (`make cover-gate`, `make shim`).

## v0.9.2 - 2026-09-09

- **New:** `add` and `enver edit` compose multi-parent `extends`
  interactively. The picker preserves selection order — parents are ranked as
  picked (`1`, `2`, …), `<`/`>` or `←`/`→` move the cursor row within the
  order, and confirming reports the chain in inheritance order. Previously
  both commands handled a single parent; multi-parent chains required YAML or
  `enver import`. In `edit`, confirming with nothing selected clears
  `extends`; the unused `ui.SelectDefault` helper was removed. Chain values
  the picker cannot offer — a parent in the other layer or a deleted one —
  appear as dimmed `(external)` rows instead of being dropped on confirm;
  `add` rejects a pick that would form an `extends` cycle, and its inherited
  summary keeps the healthy parents' keys when one ancestor is unresolvable.
- **Breaking:** encrypted values now use `enc:v3:argon2id:<t>:<m>:<p>:...` with
  KDF parameters embedded in each value. Only `enc:v3:` is readable; values
  with other `enc:` prefixes fail loudly, as do configs mixing values from
  different keys or KDF-parameter eras (decrypt and re-encrypt to unify).
  Decrypt existing `enc:v2:` values with the previous enver version before
  upgrading, then re-run `enver encrypt`. The key file is compatible as-is.
- **New:** profiles can list env keys enver must not set in the resolved
  environment (`unset: TOKEN` or a list). Unset keys drop out of `show`,
  `export`, `dotenv`, and what `enver x` overlays on your shell; values your
  shell already exports pass through untouched and stay visible to `$VAR`
  interpolation. Unsets apply where they are written: a later layer's
  definition beats an earlier layer's unset, a chain profile's unset still
  strips its ancestors' value under any local file, and the closest mention
  of a key wins. Names must be valid identifiers; matching is
  case-insensitive on Windows.
- **New:** provenance for resolved values. `enver show` annotates each key
  with `# from <profile> (<layer>)`; `--format json` reports a per-key
  `sources` map.
- `enver validate` flags a profile that both defines and unsets the same key
  in one layer (`contradictory-unset`, warning); a cross-layer pair resolves
  by ordering instead and draws no warning.
- `enver edit` manages unsets interactively: a **Manage unsets…** action opens
  a picker of candidate keys with the declared fences pre-checked (`space`,
  `-`, or `x` toggles). Fence state renders live in the menu as a faded row:
  fenced own vars and inherited keys alike stay listed, visibly dimmed
  instead of carrying a marker, so suppression is visible where it happens
  while resolution (`show`, `x`, export) still omits them. Every route into a
  same-layer define+unset pair confirms first like the `validate` warning
  suggests: picking such a key asks — declining drops just the disputed
  fences, not the rest of the pass — adding or renaming onto an
  already-fenced key asks (declining lifts the fence), and confirming a full
  wipe asks before it lands. Leaving the picker mid-edit offers to resume the
  pending toggles rather than discarding them silently; the delete picker
  marks keys whose fence would outlive the deletion; the Done row's
  unsaved-changes flag compares unset edits order-insensitively; duplicate
  entries in hand-written `unset:` lists collapse at load. `edit` previously
  round-tripped the unset list untouched.
- Env key names are validated everywhere they enter a config — YAML load,
  every write boundary, dotenv parsing, TUI input — rejecting anything outside
  `[A-Za-z_][A-Za-z0-9_]*`: names ride unquoted inside eval'd export lines.
- `enver import` marks keys fenced by the target profile's unset list as `!`
  and asks before removing fenced keys or clearing unset entries; decline
  aborts, `--force` skips the prompt, and a non-interactive pipe without
  `--force` errors instead of answering silently.
- Windows fixes: overlaying, deleting, and unsetting env names match all case
  spellings of a variable, so a stale case-variant can no longer survive an
  override or fence; extends composition stays byte-exact so `[prod]` and
  `[Prod]` remain distinct profiles everywhere.
- Crypto hardening: `keygen` verifies a typed passphrase against existing
  encrypted values before writing anything — a typo errors out instead of
  stranding them; decrypt pre-checks the key and encrypt/decrypt errors name
  the remedy; `encrypt` refuses to write values under a salt different from
  encrypted values already present in the profiles it touches, and new values
  join the KDF-parameter era of same-salt values anywhere in the file, so a
  per-profile encrypt cannot split it into two eras under one passphrase.
