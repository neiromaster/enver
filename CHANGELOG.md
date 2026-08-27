# Changelog

## Unreleased

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
  `-`, or `x` toggles). Fence state renders live in the menu (`⊘ KEY · unset`
  on own rows; fenced inherited keys leave the resolved view), adding a fence
  onto a key the profile also defines confirms first like the `validate`
  warning suggests, and the Done row's unsaved-changes flag now tracks unset
  edits. `edit` previously round-tripped the unset list untouched.
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
