# Changelog

## Unreleased

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
