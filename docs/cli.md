# CLI reference

All commands and global flags. Behavior details: [`config.md`](./config.md),
[`profiles.md`](./profiles.md), [`dotenv.md`](./dotenv.md),
[`secrets.md`](./secrets.md).

## Commands

```
enver x [profiles...] -- <command> [args...]  Run command with the composed profiles' env
enver show [profiles...] [--no-mask] [--format text|json]  Preview resolved env (masked by default)
enver export [profiles...] [--format bash|fish|powershell] Print `export K=V` (unmasked, for eval)
enver dotenv [profiles...] [-o file] [--no-header] [--force]  Write the composed profiles to a .env file (with comments)
enver import <file> [profile] [--replace]              Import a .env file into a profile (--extends, --force)
enver list [--format text|json]          List profiles
enver add [name]                          Interactively add a profile
enver edit [profile]                      Interactively edit a profile
enver remove [profile] [-y]               Delete a profile
enver rename [old] [new]                  Rename a profile (rewrites extends/default refs)
enver duplicate <src> [new]               Copy a profile (extends, env, comments)
enver default [profile] [--clear]         Set, show, or clear the default profile
enver validate                            Check config health
enver pull [<source>|<name>] [--as name]  Fetch a remote config (--local, --no-prefix, --rm, --list, --force)
enver keygen [--random] [--force]         Passphrase-derived key; --random for a random key (CI)
enver encrypt [profile] [--all]           Encrypt ~marked values (--all: every value)
enver decrypt [profile]                   Decrypt values back to plaintext
enver update [--check]                    Update the enver binary; --check reports (0/1/2 exit codes)
```

On an interactive terminal, `enver x` primes the tab title with the profile
label — the composed `base+slotA` when several profiles are given — before
exec, so the child's own title replaces it as soon as it sets one.

## Composing profiles

Read commands take several profiles: `enver x base slotA -- claude` composes
them as an anonymous `extends` — each member resolves with its own full
`extends` chain, members fold left to right, and a later member wins a shared
key. Sibling members never fence each other: an `unset` in one member does not
hide a key another member defines, while an `unset` inside a member's own
chain fences as usual. The config `default` applies only when the list is
empty; an explicit list replaces it. `show` renders the composed chain
(`# profile: base → slotA`), and the composed label `base+slotA` is what the
tab title, the JSON `profile` field, and the dotenv header carry. Completion
offers profiles at every position before `--` — already-typed members are
excluded — and after `--` the child command owns the completion.

`enver update` applies the latest release, dispatching to the package
manager that owns the install (npm, brew, go install) or replacing a
directly-installed binary from GitHub releases after verifying its SHA-256.
Exit codes: 0 up to date, 1 update available (`--check`), 2 error. The new
binary is used on the next launch.

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

With no profile arguments, the config's `default` is used. `enver show`
previews the resolved env (masked by default); `enver list` lists profiles.
Every `show` line is annotated with the defining profile and layer
(`ANTHROPIC_MODEL=claude-sonnet-5  # from anth (global)`), so with multi-parent
`extends` you can see at a glance which profile actually won a key — a
variable picked up from `./.enver.yaml` is marked `(local)`, one from the
global config `(global)`, and one a pulled remote supplied `(remote:<name>)`,
which is how a swapped endpoint becomes visible. Keys the chain deliberately
excludes render as comment rows (`# DEBUG — unset by "prod"`, detailed in
[`dotenv.md`](./dotenv.md)). `--format json` carries the same provenance as a
structured `sources` map plus an `unsets` map with the same profile/layer
shape, so a machine reader can tell a deliberately unset key from an
accidental absence. A key whose ciphertext this key cannot read is reported
per key instead of rendered: `show` shows `(cannot decrypt: …)` on its row,
`--format json` carries an `undecryptable` map (`key → reason`) and leaves
those keys out of `env` entirely. A text `show` tolerates a missing key; JSON
still fails on one. A key that is present but cannot read a value a remote
supplied is the exception — it degrades per key in both formats (see
[`secrets.md`](./secrets.md#ciphertext-someone-else-wrote)).
`enver list` marks a profile whose chain cannot resolve the same way — its
vars cell reads `N (→?)` instead of a resolved count — while `--format json`
errors instead of listing a partial config.

In a terminal, text output is styled: keys are bold, and every comment — the
`# profile:` header, provenance suffixes, unset rows — is dim. Styling is
emitted only when the output destination is a terminal; `NO_COLOR` and
`TERM=dumb` turn it off, so piped output (`enver show | grep`) stays plain.
`--format json` is never styled.

A value counts as a secret when the key carries a leading `~` mark or the
value embeds credentials in a URL such as `postgres://user:pass@host` — a
key's name never matters (see
[`secrets.md`](./secrets.md#marking-arbitrary-values)). `enver show` masks
those values and `enver encrypt` seals them (use `--no-mask` to reveal); a
value already stored as `enc:v3:` stays masked and is skipped by `encrypt`.
Plain URLs without credentials (`https://api.example.com`) are left alone.
Masking is text-only: a marked key whose value is still plaintext is masked and
annotated `~ marked, not encrypted`, while the file itself stays as it is.
`enver export` is always unmasked so `eval "$(enver export <profile>)"` applies
to the current shell. `--format fish` emits `set -gx K 'V'` for fish
(`enver export <profile> --format fish | source`); `--format powershell`
emits `$env:K = 'V'` for PowerShell
(`enver export <profile> --format powershell | iex`); `bash` is the default.
`show`/`list` also accept `--format json` for machine-readable output (`show`
still honors `--no-mask`).

Profile names may collide with subcommand verbs (`x`, `show`, `export`,
`dotenv`, `import`, `list`, `add`, `edit`, `remove`, `rename`, `duplicate`,
`default`, `validate`, `pull`, `keygen`, `encrypt`, `decrypt`, `update`,
`completion`); `enver x <name> -- <command>` addresses such a profile directly.

## Remotes

`enver pull` is the only command that fetches config: it fetches a source,
validates it, and stores it as the lowest layer, so `show`, `x`, `list` and the
TUI keep working offline against the stored copy. (Only `enver update` reaches
the network otherwise, and never for config.)

```
enver pull github:someone/llm-profiles --as llm  register a source and fetch it
enver pull gist:someone/abc123 --as llm          register a source and fetch it
enver pull llm                                   re-fetch one remote
enver pull                                       re-fetch every registered remote
enver pull --list                                list records with their source and last pull
enver pull --rm llm                              remove the record and its content
```

A fetched file may declare its own `remotes:` records, and one pull fetches
the whole tree it declares. Pulling a provider that declares the base it
extends stores both under the record's path, and the summary indents the
subtree:

```
✓ remote "ollama" ← github:someone/ollama-profiles at ref v2
  profiles: ollama/glm-models
  commit: 1f0c9ab
  ✓ remote "ollama/claude" ← github:someone/claude-base at ref v1
    added
    profiles: ollama/claude/default
    commit: 90e77c1
```

`ollama/glm-models` extends `ollama/claude/default` with no local wiring. The
tree re-fetches, lists and removes through the top-level record: `--list`
indents each declared child under its declarer, `--rm` removes the record's
whole stored subtree, and a re-pull names the children it added, re-pointed or
removed. Nested records are not registered in any config, so completion never
offers one and a nested path cannot be addressed by name.

Argument completion offers the registered record names, so `enver pull <Tab>`
lists what can be re-fetched or removed. Registration writes the global config;
`--local` writes the project's `.enver.yaml` instead, so the record travels with
the repo. A remote's profiles load as `<name>/<profile>`; `--no-prefix` stores
them under their bare upstream names, and a re-pull never changes the mode a
record was stored with. `--rm` removes the closest declaration in the merged
view (`-g`/`--local` target an explicit layer) and deletes the content only when
no layer still declares the name. `--list` is the read-only freshness view:
each record with its source, whether it is pinned, and the commit and time of
its last pull. A re-registration that changes the source or the mode of a record
your patches sit under is a takeover: pull warns and asks first, and `--force`
accepts it without asking; a re-fetch that would replace content the sidecar
says came from another source passes the same gate. Records, projection, the pull
gate, the divergence warning and the patch flow are documented in
[`config.md`](./config.md#remotes).

## Removed forms

> **Breaking:** the bare forms `enver <profile> -- <command>` and
> `enver <profile>` (preview) were removed. Use `enver x <profile> --
> <command>` to run, and `enver show <profile>` to preview. `enver run` was
> renamed to `enver x`.

> **Breaking:** `enver init` was renamed to `enver add` — `init` implied
> initialization, but the command only adds a profile.
