# Secrets at rest

Secrets in the config can be encrypted so the config is safe to commit to a
dotfiles repo. Only individual secret values are encrypted — keys, structure
and non-secret values (base URLs, model names) stay plaintext. "Secret" means
what the name or value heuristic flags, or what you mark with a leading `~` on
the key (see [Marking arbitrary values](#marking-arbitrary-values)).

```sh
enver keygen                    # prompt for a passphrase, write the key cache (mode 0600)
enver keygen --random           # non-interactive: write a random key cache instead (for CI)
enver encrypt                   # encrypt secret and ~marked values in the config
enver encrypt glm --all         # encrypt every value in the "glm" profile
enver decrypt                   # restore plaintext (the ~ marks stay)
```

## Key generation

`enver keygen` prompts for a passphrase twice and writes a key cache to
`~/.config/enver/key` (JSON, mode `0600`). The same passphrase always derives
the same key (argon2id; the salt comes from your encrypted values), so the key
can be regenerated from memory on any machine. When the configs already hold
encrypted values, the passphrase is verified against them — a typo errors out
instead of silently deriving a key that cannot read them. A passphrase that
*does* decrypt them is never treated as stranding, so `enver keygen --force`
with the original passphrase is the recovery path for a lost or rotated key
cache. `enver keygen --random` writes a random key cache instead —
non-derivable, for CI and other non-interactive setups.

`enver keygen --force` refuses to overwrite silently: when the configs contain
encrypted values and the new key differs from the current one, the interactive
path asks for confirmation and `--random` prints a warning, since an
overwritten key makes the existing ciphertext unreadable. Run `enver decrypt`
with the old key first if you need to migrate them.

## Marking arbitrary values

Encryption and masking key off names
(`key|token|secret|password|passwd|auth|credential`) and values that embed URL
credentials. A leading `~` on an env key marks any other variable a secret
candidate — a path carrying a username, a login, an internal hostname:

```yaml
profiles:
  work:
    env:
      ~PROJECT_PATH: /home/anna/enver
      API_BASE: https://api.example.com
```

`~` is file syntax, not part of the name: this entry defines the runtime
variable `PROJECT_PATH`, and `x`, `export`, `dotenv` and `show` only ever spell
the bare name. Exactly one `~` is allowed, the rest of the key must be a valid
variable name (a bare `~` is rejected), and one profile cannot spell the same
variable both ways — `~K` beside `K` is an error. On an `unset:` entry the mark
is accepted and ignored: a fence has nothing to encrypt.

A mark is a hint, never enforcement — it only widens what `enver encrypt`
selects. A marked value stays plaintext in the file until you run that encrypt
step yourself; `show` masks it whatever its name looks like, and flags the
plaintext state, so the file never looks safer than it is:

```console
$ enver show work
PROJECT_PATH=/hom…(len=16)  # from work (global) ~ marked, not encrypted
```

`enver encrypt` seals marked keys under their marked spelling
(`~PROJECT_PATH: enc:v3:…`), under the same key and KDF-era rules as any other
value, and the annotation disappears once the value is ciphertext. `enver
decrypt` restores the plaintext and **keeps the mark**, so a marked variable
stays a candidate and the next `encrypt` picks it up again. Remove the `~` to
drop the key from the candidate set for good — a value that is already sealed
stays sealed and masked until you `decrypt` it, so un-marking is not
un-encrypting. While a marked key is stored in plaintext, `enver validate`
reports a warning (`"PROJECT_PATH" is marked secret but stored in plaintext`);
warnings do not change its exit code.

### The mark follows the winning entry

A mark belongs to an entry, so the closest definition wins it along with the
value — across the two layers and along an `extends` chain alike. The idiom for
a machine-local value that should stay readable is therefore a closer unmarked
definition: the shared global entry keeps the key sealed and masked, while the
local file writes it without `~`.

```yaml
# ./.enver.yaml
profiles:
  work:
    env:
      PROJECT_PATH: /Users/anna/projects/enver
```

`enver show work` prints that path in the clear and stops flagging it, and
`enver encrypt` leaves it alone: the winning entry is not a candidate.

### Marking from the interactive commands

`add` and `edit` accept a leading `~` in the variable-name field, and `edit`
sets marks through its **Secrets…** menu, which covers own and inherited
variables; confirming stages the toggles and only **Done** writes them. The
menus spell a marked key as `~KEY`, so the mark is visible where it is set: the
file keeps the spelled key, while the runtime name stays bare in `x`, `export`,
`dotenv` and `show`. A mark saved this way is still only a mark — the value
stays plaintext until `encrypt` runs. The menu's **Encrypt now** runs after that
commit, against the file the commit just wrote.

`enver edit` decrypts encrypted values when a key resolves and re-encrypts them
on save, so changing a sealed value no longer needs an `enver decrypt` round
trip first. Without a key the ciphertext stays as it is and its card refuses to
open (`cannot decrypt: no key`); `add` likewise refuses to overwrite an
encrypted value it cannot reproduce. A marked value that is still plaintext
stays editable either way. Under `ENVER_KEY` the key carries no salt, so it
decrypts but cannot seal: `enver encrypt` refuses on that ground, Encrypt now
saves the marks without encrypting and says so, and a profile holding encrypted
values cannot be saved from `edit` at all until a salted key (the key cache, or
`--key`) is available.

### Without a key

Display and execution part ways when no key source resolves. Text `show` is a
display: an unreadable value renders as `KEY=(cannot decrypt: no key — run
enver keygen)` and the command still exits 0, naming what it could not read
instead of printing ciphertext. `show --format json`, `export`, `dotenv`, `x`,
`encrypt` and `decrypt` are execution paths — handing a child or a machine a
missing secret is worse than refusing — so they fail, naming the keys that
blocked them. An absent key degrades or refuses as above; a key that is present
but wrong is a misconfiguration and fails everywhere.

## Value format and era rules

Encrypted values are
`enc:v3:argon2id:<t>:<m-KiB>:<p>:<base64(salt||nonce||ciphertext)>`
(AES-256-GCM, argon2id). KDF parameters travel inside every value, so
passphrase recovery survives future parameter upgrades — after such an
upgrade, re-encrypt the whole file (recovery derives the key from the first
value; a file mixing parameter eras only partially decrypts). Values with
other `enc:` prefixes (older formats) are rejected with an error, in every
profile. `enver encrypt` refuses to write values under a key whose salt
differs from the encrypted values already present in the profiles it touches —
install the matching key first (keygen --force with the original passphrase,
or restore its key file; decrypt pre-checks the key and names the same
remedy); targeting one profile is not blocked by stranded different-key values
in profiles you are not touching. Scoped encrypt beside stranded values is
allowed by design and leaves the file mixed-salt, which makes keygen refuse
until the stranded values are removed. New values join the KDF-parameter era
of same-salt values anywhere in the file, so a per-profile encrypt cannot
split the file into two eras under one passphrase.

## Runtime resolution

At runtime `enver x <profile> -- <command>` **transparently decrypts** with no
prompt, so the day-to-day command is unchanged. The key is resolved in this
order: `--key <path>` flag, `ENVER_KEY` env var (base64, for CI — decryption
only, since it carries no salt), the default key file, then an interactive
passphrase prompt that derives, verifies, and caches the key. Where stdin is
not a terminal the prompt is skipped and the command fails loudly instead of
hanging. A profile with no encrypted values runs without any key.

**The exec path drops `ENVER_KEY` from the child environment** — the child gets
the decrypted values, not the key. When a set key was dropped, `enver x` prints
a one-line stderr note naming the opt-out. Nested enver invocations under the
child (a Makefile calling `enver` inside `enver x -- make`) opt back in with
`ENVER_FORWARD_KEY=1`: the resolved key — whatever source it came from (`--key`,
the env var, or the key cache) — is re-exported as the child's `ENVER_KEY`,
carrying no salt, so nested runs can decrypt but not encrypt. The flag rides
along with the child, so one opt-in covers the whole process tree. In CI,
prefer placing the key file at `~/.config/enver/key` instead: children read it
directly, with no key material in any environment.

**Recovery on a new machine:** clone the encrypted config, then run
`enver x <profile> -- <command>` (or `enver encrypt` / `enver decrypt`) and
enter the passphrase when prompted — the key is derived from the salt embedded
in your values, verified against them, and cached to `~/.config/enver/key`.
From then on `enver x` runs without prompting.

> Commit the encrypted config; never commit the key file. Encryption protects
> against accidental leaks (git, dotfiles, casual disk access), not against an
> attacker with read access to both the config and the key on the same machine.

## Threat model

- **Secrets at rest** — `enver encrypt` stores values as `enc:v3:` ciphertext
  (AES-256-GCM, argon2id with parameters embedded). Values with other `enc:`
  prefixes (older formats) are rejected. The key cache lives at
  `~/.config/enver/key` (mode `0600`) — **never commit the key**.
- **Preview masking** — `enver show` redacts a value whose key is marked with
  `~`, whose value is encrypted at rest, or whose name matches
  `key|token|secret|password|passwd|auth|credential`; use `--no-mask` or
  `enver export` to reveal them. Masking is display only — the file stays as it
  is — so a marked key still in plaintext is flagged, not fixed.
- **Scope** — encryption protects against accidental leaks (git, dotfiles,
  casual disk access), not against an attacker with read access to both the
  config and the key on the same machine. For stronger key storage, keep the
  key out of the repo and inject it via `ENVER_KEY` in CI.

To report a vulnerability, open a private security advisory on GitHub.
