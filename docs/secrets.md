# Secrets at rest

Secrets in the config can be encrypted so the config is safe to commit to a
dotfiles repo. Only individual secret-looking values are encrypted — keys,
structure and non-secret values (base URLs, model names) stay plaintext.

```sh
enver keygen                    # prompt for a passphrase, write the key cache (mode 0600)
enver keygen --random           # non-interactive: write a random key cache instead (for CI)
enver encrypt                   # encrypt secret-looking values in the config
enver encrypt glm --all         # encrypt every value in the "glm" profile
enver decrypt                   # restore plaintext (for editing)
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
- **Preview masking** — `enver show` redacts
  `key|token|secret|password|auth|credential` values; use `--no-mask` or
  `enver export` to reveal them.
- **Scope** — encryption protects against accidental leaks (git, dotfiles,
  casual disk access), not against an attacker with read access to both the
  config and the key on the same machine. For stronger key storage, keep the
  key out of the repo and inject it via `ENVER_KEY` in CI.

To report a vulnerability, open a private security advisory on GitHub.
