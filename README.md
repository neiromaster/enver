# enver

[![CI](https://github.com/neiromaster/enver/actions/workflows/ci.yml/badge.svg)](https://github.com/neiromaster/enver/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/neiromaster/enver.svg)](https://pkg.go.dev/github.com/neiromaster/enver)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)

Inject environment variables from named, layered YAML profiles into any child
command — **without mutating the target tool's own config**. Built for running
[Claude Code](https://github.com/anthropics/claude-code) against different
API keys / base URLs / model sets, but works for any command.

```
enver x -- claude                       # uses the config's default profile
enver x openrouter -- claude --model claude-sonnet-5
eval "$(enver export prod-db)"
enver add                   # interactively create a profile
enver encrypt               # encrypt secret values at rest
```

## Why

`dotenvx` / `dotenv` load `.env` files local to the working directory. `direnv`
layers by directory but via a shell hook and per-dir files. Existing
claude-specific switchers (`ccm`, `claude-code-switcher`) mutate
`~/.claude/settings.json` or hardcode the `ANTHROPIC_*` schema.

`enver` is a **provider-agnostic exec shim**: one YAML store, profiles that can
inherit via `extends`, layered `cwd → home`, and a clean
`enver x <profile> -- <command>` invocation that injects env only into the child
process.

## Install

**Homebrew** (macOS & Linux):

```sh
brew tap neiromaster/enver
brew trust neiromaster/enver   # Homebrew requires trusting third-party taps
brew install enver
```

The `enver` cask installs the `enver` binary and wires up shell completions
(bash, zsh, fish) automatically. On macOS the cask also strips Gatekeeper
quarantine, so the binary runs without a manual "Allow Anyway" step.

**Go** (anywhere with a Go toolchain):

```sh
go install github.com/neiromaster/enver/cmd/enver@latest
```

This drops the `enver` binary into `$GOBIN` (on your `PATH`). Or
build from source without installing:

```sh
git clone https://github.com/neiromaster/enver && cd enver && make build   # → ./bin/enver
```

`make build` produces `enver`. Pre-compiled binaries for linux/darwin/windows ×
amd64/arm64 are on the [releases page](https://github.com/neiromaster/enver/releases);
each archive includes the `enver` binary and shell completions.

## Config

Global config: `$XDG_CONFIG_HOME/enver/config.yaml`
(default `~/.config/enver/config.yaml`). See [`config.example.yaml`](./config.example.yaml).

```yaml
default: anth

profiles:
  anth:
    env:
      ANTHROPIC_API_KEY: sk-ant-...
      ANTHROPIC_BASE_URL: https://api.anthropic.com
      ANTHROPIC_MODEL: claude-sonnet-5
      ANTHROPIC_SMALL_FAST_MODEL: claude-haiku-4-5-20251001

  openrouter:
    env:
      ANTHROPIC_BASE_URL: https://openrouter.ai/api/v1
      ANTHROPIC_API_KEY: sk-or-...
      ANTHROPIC_MODEL: anthropic/claude-sonnet-5

  local-proxy:
    extends: anth                 # inherit anth's env, override below
    env:
      ANTHROPIC_BASE_URL: http://localhost:8082
```

### Layering

`enver` uses exactly two layers: the global config above plus a local
`./.enver.yaml` in the current directory (no walk-up — only that one file). When
both define the same profile, env keys overlay per-key (local wins) and
`extends` lists concatenate as `[global…, local…]` (deduped), so local mixins
compose with rather than replace global ones. Use the local file for
project-specific overrides, or for profiles that extend global ones:

```yaml
# ./.enver.yaml — a project profile built on the global `anth`
profiles:
  dev:
    extends: anth
    env:
      ANTHROPIC_MODEL: claude-opus-5   # override just the model
```

Merge rules: `default` is overridden when set; profiles union; per-profile env
keys are overridden per-key; `extends` lists merge as `[global…, local…]`, so a
local parent wins over a global parent on a shared key.

### Inheriting from multiple profiles

A profile can extend several parents to compose mixins — small, single-purpose
profiles combined into one:

```yaml
profiles:
  proxy:        { env: { ANTHROPIC_BASE_URL: http://localhost:8082 } }
  opus-models:  { env: { ANTHROPIC_MODEL: claude-opus-5 } }
  dev:
    extends: [proxy, opus-models]
    env:
      ANTHROPIC_API_KEY: sk-...
```

Precedence: parents are applied **left-to-right, so a later parent overrides an
earlier one** on a shared key; the profile's own env overrides all parents;
inheritance is transitive. The common single-parent form (`extends: anth`) is
unchanged.

A diamond where two parents both extend a shared grandparent, and one overrides
a key the other only inherits, can let the later parent's inherited value win
over an earlier parent's explicit override. This is consistent with the
left-to-right rule and does not affect orthogonal mixins; `enver validate` does
not warn on overlaps.

**Authoring multiple parents:** `add` and `edit` select a single parent. Set
several by editing the YAML (`extends: [a, b]`) or via
`enver import <file> <name> --extends a,b`.

### Removing variables (`unset`)

Inheritance is additive — a child can override an inherited key but not remove
it. A profile can also list keys enver must not set in the resolved
environment:

```yaml
profiles:
  bare:
    extends: anth
    unset: [ANTHROPIC_API_KEY]   # never set this key from the config
```

`unset` accepts a single key (`unset: ANTHROPIC_API_KEY`) or a list. It keeps
the key out of the profile's resolved env (so `show`, `export`, and `dotenv`
drop it) and out of what `enver x` overlays on your shell. It does **not**
remove a key your shell already exports: `enver x bare -- claude` leaves a
live `ANTHROPIC_API_KEY` untouched, and `eval "$(enver export bare)"` assigns
nothing for it. To actually delete a variable, remove it from your shell before
running enver. Env key and unset names must be valid identifiers
(`[A-Za-z_][A-Za-z0-9_]*`, the same rule `.env` import applies): names ride
unquoted inside eval'd export lines, so enver refuses anything looser at config
load, at every write boundary, and at TUI input time.
`$VAR` interpolation sees the shell's live value through an unset: a key enver
does not set is simply absent from the resolved env, so a reference to it
expands to whatever the shell exports — matching what the child process gets.

Unsets union across layers (global and local both apply) and the closest
mention of a key wins: a profile's own unset strips a parent's definition, and
a closer redefinition overrides an ancestor's unset. Author `unset` in YAML
like multi-parent `extends` — a single name or a list, never a mapping;
`enver validate` flags an `unset` naming a key the same profile also sets in
the same layer's env (`contradictory-unset` — a local unset fencing a global
definition is the intended cross-layer pattern). On Windows, where env names
are case-insensitive, unset matching is case-insensitive too.

## Creating profiles interactively

`enver add` walks you through a new profile and writes it into the global
config, preserving any existing structure and comments:

- **Profile name** — letters, digits, `-`, `_`; must start with a letter or digit.
  Pass it as an argument (`enver add glm`) to skip this prompt.
- **Extends** — picked from a list of existing profiles (with a `(none)` option).
  Skipped when no profiles exist yet.
- **Variables** — for each, enter a name, a value, and an optional comment; leave
  the name blank to finish. A profile needs at least one variable or an extends.
- **Default** — optionally set as the default profile.

The optional comment is stored as a YAML comment above the entry. It survives
`enver encrypt`, so you can keep a plaintext hint (e.g. a link to the secret
store) above an encrypted value:

```yaml
profiles:
  glm:
    env:
      # get this token from https://vault.example/anth
      ANTHROPIC_API_KEY: enc:v3:argon2id:...
```

Env keys merge additively into an existing profile of the same name.

Mutating commands (`add`, `edit`, `remove`, `rename`, `duplicate`, `default`,
`import`, `encrypt`, `decrypt`) write to `./.enver.yaml` by default; pass
`--global` (`-g`) to write the user config instead. `enver add` creates the
local file if it is absent. When authoring locally, the `extends` picker lists
both local and global profiles; under `--global` it lists global profiles only
(a global profile must not extend a local one, which would break outside this
directory).

Profile completion follows the same split: read commands (`show`, `export`,
`dotenv`, `x`) complete against both layers, while mutating commands complete
only the profiles they can write — the local file, or the global file under
`--global`.

## Editing profiles

`enver edit` opens a profile in an interactive menu where the profile's own
variables are listed and editable, while variables inherited through `extends`
are shown read-only (marked `(inherited)`) — change those in the profile they
come from. Nothing is written until you select **Done**, so you can experiment
freely. Press **ESC** at any time to cancel the current prompt and return to
the menu:

- **Add or edit a variable** — select an existing var to change its value or
  comment, or add a new one (name, value, optional comment, as in `add`).
- **Change extends** — repoint the profile at another profile or clear it; a
  choice that would form an `extends` cycle is rejected when you commit.
- **Toggle default** — set or clear this profile as the default.
- **Delete variable** — remove one or more of the profile's own variables.
- **Delete profile** — remove the whole profile. It is refused while other
  profiles extend it; if the profile is the default, the default is cleared
  along with it (see `remove`).

Cancelling exits without writing. A profile must keep at least one variable or
an `extends`, so **Done** is rejected on an empty profile.

## Managing profiles

- **`enver remove [profile]`** — delete a profile. Refused while other profiles
  extend it (the error names the dependents); repoint or remove those first. If
  the profile is the default, the default is cleared with it. Pass `--yes` / `-y`
  to skip the confirmation prompt.
- **`enver rename [old] [new]`** — rename a profile and rewrite every reference
  in the same file: any `extends: old` in other profiles and the top-level
  `default` if it matches. Refused if the new name already exists. (References
  in the *other* layer are not rewritten; `validate` surfaces any that go
  dangling.)
- **`enver duplicate <src> [new]`** — make a structural copy of a profile
  (extends, env, and comments). The copy is not made the default.
- **`enver default [profile]`** — with no argument, print the current default;
  with a name, set it; `--clear` removes the default. The effective default is
  the local file's `default` when set, otherwise the global file's — a project
  can pin its own default while keeping the user-level one as fallback.
- **`enver validate`** — audit config health: dangling `extends` and `extends`
  cycles (errors), and profiles with no env and no extends (warnings). It also
  checks the global file in isolation and flags a global profile that extends a
  local-only name (fine in this directory, broken elsewhere). Exits non-zero if
  any error is found.

## Exporting and importing `.env`

`enver dotenv` writes a profile as a standard `.env` file — values decrypted
and unmasked, per-key comments preserved — and `enver import` reads one back
into a profile. They round-trip: hand a profile to any tool that expects `.env`,
then bring the result back.

```sh
enver dotenv prod -o prod.env          # export profile prod to prod.env
enver import prod.env staging          # import into a new profile (merged)
enver import prod.env prod --replace   # reset prod to exactly prod.env (confirms)
```

Imported values are stored **raw** — `$VAR` references stay as templates, not
expanded — so layered profiles survive the round-trip. `import` **merges** by
default (imported keys override existing same-named keys); `--replace` wipes the
profile's own env and `unset` list first and confirms when it would remove keys
or clear unset entries
(decline to abort; `--force` skips the prompt; a non-interactive pipe without
`--force` errors).
`--extends <profile>` sets or overrides `extends` (otherwise it is preserved,
including across `--replace`); an empty import without `--extends` is refused.
The summary prints a diff of what changed: `+` added, `~` overridden,
`-` removed (values shown in full — the data came from your own .env file).
 Keys the profile's own `unset` list fences are marked `!` — written, but never reaching `show`, `export`, or `enver x`.

> `dotenv -o` and `import` move decrypted secrets through a plaintext `.env`
> file. Mind where it lands; keep values at rest encrypted with `enver encrypt`.

## Encrypting secrets

Secrets in the config can be encrypted at rest so the config is safe to commit
to a dotfiles repo. Only individual secret-looking values are encrypted — keys,
structure and non-secret values (base URLs, model names) stay plaintext.

```sh
enver keygen                    # prompt for a passphrase, write the key cache (mode 0600)
enver keygen --random           # non-interactive: write a random key cache instead (for CI)
enver encrypt                   # encrypt secret-looking values in the config
enver encrypt glm --all         # encrypt every value in the "glm" profile
enver decrypt                   # restore plaintext (for editing)
```

`enver keygen` prompts for a passphrase twice and writes a key cache to
`~/.config/enver/key` (JSON, mode `0600`). The same passphrase always derives
the same key (argon2id; the salt comes from your encrypted values), so the key
can be regenerated from memory on any machine. When the configs already hold
encrypted values, the passphrase is verified against them — a typo errors out
instead of silently deriving a key that cannot read them. A passphrase that *does* decrypt them is never treated as stranding, so `enver keygen --force` with the original passphrase is the recovery path for a lost or rotated key cache. `enver keygen
--random` writes a random key cache instead — non-derivable, for CI and other
non-interactive setups.

`enver keygen --force` refuses to overwrite silently: when the configs contain
encrypted values and the new key differs from the current one, the interactive
path asks for confirmation and `--random` prints a warning, since an overwritten
key makes the existing ciphertext unreadable. Run `enver decrypt` with the old
key first if you need to migrate them.

Encrypted values are `enc:v3:argon2id:<t>:<m-KiB>:<p>:<base64(salt||nonce||ciphertext)>`
(AES-256-GCM, argon2id). KDF parameters travel inside every value, so
passphrase recovery survives future parameter upgrades — after such an
upgrade, re-encrypt the whole file (recovery derives the key from the first
value; a file mixing parameter eras only partially decrypts). Values with
other `enc:` prefixes (older formats) are rejected with an error, in every
profile. `enver encrypt` refuses to write values under a key whose salt differs
from the encrypted values already present in the profiles it touches — install
the matching key first (keygen --force with the original passphrase, or restore its key file; decrypt pre-checks the key and names the same remedy); targeting one profile is not blocked by stranded
different-key values in profiles you are not touching. Scoped encrypt beside stranded values is allowed by design and leaves the file mixed-salt, which makes keygen refuse until the stranded values are removed. New values join the KDF-parameter era of same-salt values anywhere in the file, so a per-profile encrypt cannot split the file into two eras under one passphrase.

At runtime `enver x <profile> -- <command>` **transparently decrypts** with no
prompt, so the day-to-day command is unchanged. The key is resolved in this
order: `--key <path>` flag, `ENVER_KEY` env var (base64, for CI — decryption
only, since it carries no salt), the default
key file, then an interactive passphrase prompt that derives, verifies, and
caches the key. Where stdin is not a terminal the prompt is skipped and the
command fails loudly instead of hanging. A profile with no encrypted values runs
without any key.

**Recovery on a new machine:** clone the encrypted config, then run
`enver x <profile> -- <command>` (or `enver encrypt` / `enver decrypt`) and
enter the passphrase when prompted — the key is derived from the salt embedded
in your values, verified against them, and cached to `~/.config/enver/key`. From
then on `enver x` runs without prompting.

> Commit the encrypted config; never commit the key file. Encryption protects
> against accidental leaks (git, dotfiles, casual disk access), not against an
> attacker with read access to both the config and the key on the same machine.

## Usage

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
enver --global / -g                       Write to the global config (default: ./.enver.yaml)
enver --config <path>                     Override the global config file (read path; --global writes)
enver --key <path>                        Key file (or ENVER_KEY env)
enver --no-local                          Ignore the ./.enver.yaml layer when reading
enver --no-expand                         Do not expand $VAR references
enver --chdir <dir>                       Run as if started from <dir> (.enver.yaml and relative --config resolve against it)
enver --version / -h, --help
```

> **Breaking:** the bare forms `enver <profile> -- <command>` and `enver <profile>`
> (preview) were removed. Use `enver x <profile> -- <command>` to run, and
> `enver show <profile>` to preview. `enver run` was renamed to `enver x`.

> **Breaking:** `enver init` was renamed to `enver add` — `init` implied initialization, but the command only adds a profile.

With no profile, the config's `default` is used. `enver show <profile>` previews
the resolved env (masked by default); `enver list` lists profiles. Every `show`
line is annotated with the defining profile and layer
(`ANTHROPIC_MODEL=claude-sonnet-5  # from anth (global)`), so with multi-parent
`extends` you can see at a glance which profile actually won a key — a variable
picked up from `./.enver.yaml` is marked `(local)`, one from the global config
`(global)`. `--format json` carries the same provenance as a structured
`sources` map.

Profile names may collide with subcommand verbs (`x`, `show`, `export`,
`dotenv`, `import`, `list`, `add`, `edit`, `remove`, `rename`, `duplicate`,
`default`, `validate`, `keygen`, `encrypt`, `decrypt`, `completion`);
`enver x <name> -- <command>` addresses such a profile directly.

Secret-looking values (keys matching `key|token|secret|password|auth|credential`,
case-insensitive, or values that embed credentials in a URL such as
`postgres://user:pass@host`) are masked in `enver show` output (use `--no-mask`
to reveal) and encrypted by `enver encrypt`. Plain URLs without credentials
(`https://api.example.com`) are left alone. `enver export` is always unmasked so
`eval "$(enver export <profile>)"` applies to the current shell. `--format
fish` emits `set -gx K 'V'` for fish (`enver export <profile> --format fish |
source`); `--format powershell` emits `$env:K = 'V'` for PowerShell (`enver
export <profile> --format powershell | iex`); `bash` is the default.
`show`/`list` also accept
`--format json` for machine-readable output (`show` still honors `--no-mask`).

## How it works

1. Load the global config, then overlay the local `./.enver.yaml`.
2. Resolve the selected profile (or `default`), walking `extends` with cycle
   detection — root applied first, child overrides parent.
3. For `enver x`: child env = current `os.Environ()` ⊕ profile env,
   then `exec` the command with stdin/out/err connected and the child's exit code
   propagated. On a terminal, enver first primes the tab title (VS Code agent
   mode + profile name) so the child's own title shows live. Windows has no
   `exec`, so there the child is spawned and waited on instead.

No file under `~/.claude/` or elsewhere is modified.

## Security

- **Secrets at rest** — `enver encrypt` stores values as `enc:v3:` ciphertext
  (AES-256-GCM, argon2id with parameters embedded). Values with other `enc:`
  prefixes (older formats) are rejected. The key cache lives at
  `~/.config/enver/key` (mode `0600`) — **never commit the key**.
- **Preview masking** — `enver show` redacts `key|token|secret|password|auth|credential`
  values; use `--no-mask` or `enver export` to reveal them.
- **Threat model** — encryption protects against accidental leaks (git,
  dotfiles, casual disk access), not against an attacker with read access to
  both the config and the key on the same machine. For stronger key storage,
  keep the key out of the repo and inject it via `ENVER_KEY` in CI.

To report a vulnerability, open a private security advisory on GitHub.

## Contributing

enver is a thin, domain-agnostic shim by design — features that encode knowledge
of the launched command (e.g. API health probes) belong elsewhere. Bug reports,
docs and tests are welcome. Run checks locally:

```sh
make vet test
```

A pre-commit hook (via [lefthook](https://github.com/evilmartians/lefthook))
auto-formats staged Go files. Enable it once after cloning — lefthook and
golangci-lint are pinned as Go tool dependencies in the isolated `tools/`
module (reached through `go.work`), so nothing global is required:

```sh
make hooks   # runs `go tool lefthook install`
```

Keep `tools/` free of .go files: the tools module must stay invisible to
`go build`/`go test`/`go vet` run from the repo root.

The `use` order in `go.work` matters too: the repo root must stay first so
release tooling treats it as the main module.

CI also runs `gofmt` and `golangci-lint`, so unformatted code won't merge.

Commits follow [Conventional Commits](https://www.conventionalcommits.org/).

## License

MIT — see [LICENSE](./LICENSE).