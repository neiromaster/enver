# CLI reference

All commands and global flags. Behavior details: [`config.md`](./config.md),
[`profiles.md`](./profiles.md), [`dotenv.md`](./dotenv.md),
[`secrets.md`](./secrets.md).

## Commands

```
enver x [profile] -- <command> [args...]  Run command with the profile's env
enver show [profile] [--no-mask] [--format text|json]  Preview resolved env (masked by default)
enver export [profile] [--format bash|fish|powershell] Print `export K=V` (unmasked, for eval)
enver dotenv [profile] [-o file]                       Write a profile to a .env file (with comments)
enver import <file> [profile] [--replace]              Import a .env file into a profile (--extends, --force)
enver list [--format text|json]          List profiles
enver add [name]                          Interactively add a profile
enver edit [profile]                      Interactively edit a profile
enver remove [profile] [-y]               Delete a profile
enver rename [old] [new]                  Rename a profile (rewrites extends/default refs)
enver duplicate <src> [new]               Copy a profile (extends, env, comments)
enver default [profile] [--clear]         Set, show, or clear the default profile
enver validate                            Check config health
enver keygen [--random] [--force]         Passphrase-derived key; --random for a random key (CI)
enver encrypt [profile] [--all]           Encrypt secret values in the config
enver decrypt [profile]                   Decrypt values back to plaintext
```

On an interactive terminal, `enver x` primes the tab title with the profile
name before exec, so the child's own title replaces it as soon as it sets one.

## Global flags

```
enver --global / -g                       Write to the global config (default: ./.enver.yaml)
enver --config <path>                     Override the global config file (read path; --global writes)
enver --key <path>                        Key file (or ENVER_KEY env)
enver --no-local                          Ignore the ./.enver.yaml layer when reading
enver --no-expand                         Do not expand $VAR references
enver --chdir <dir>                       Run as if started from <dir> (.enver.yaml and relative --config resolve against it)
enver --version / -h, --help
```

## Preview and masking

With no profile, the config's `default` is used. `enver show <profile>`
previews the resolved env (masked by default); `enver list` lists profiles.
Every `show` line is annotated with the defining profile and layer
(`ANTHROPIC_MODEL=claude-sonnet-5  # from anth (global)`), so with multi-parent
`extends` you can see at a glance which profile actually won a key — a
variable picked up from `./.enver.yaml` is marked `(local)`, one from the
global config `(global)`. `--format json` carries the same provenance as a
structured `sources` map.

Secret-looking values (keys matching `key|token|secret|password|auth|credential`,
case-insensitive, or values that embed credentials in a URL such as
`postgres://user:pass@host`) are masked in `enver show` output (use
`--no-mask` to reveal) and encrypted by `enver encrypt`. Plain URLs without
credentials (`https://api.example.com`) are left alone. `enver export` is
always unmasked so `eval "$(enver export <profile>)"` applies to the current
shell. `--format fish` emits `set -gx K 'V'` for fish
(`enver export <profile> --format fish | source`); `--format powershell`
emits `$env:K = 'V'` for PowerShell
(`enver export <profile> --format powershell | iex`); `bash` is the default.
`show`/`list` also accept `--format json` for machine-readable output (`show`
still honors `--no-mask`).

Profile names may collide with subcommand verbs (`x`, `show`, `export`,
`dotenv`, `import`, `list`, `add`, `edit`, `remove`, `rename`, `duplicate`,
`default`, `validate`, `keygen`, `encrypt`, `decrypt`, `completion`);
`enver x <name> -- <command>` addresses such a profile directly.

## Removed forms

> **Breaking:** the bare forms `enver <profile> -- <command>` and
> `enver <profile>` (preview) were removed. Use `enver x <profile> --
> <command>` to run, and `enver show <profile>` to preview. `enver run` was
> renamed to `enver x`.

> **Breaking:** `enver init` was renamed to `enver add` — `init` implied
> initialization, but the command only adds a profile.
