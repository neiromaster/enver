# Changelog

## Unreleased

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
