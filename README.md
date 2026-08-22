# enver

[![CI](https://github.com/neiromaster/enver/actions/workflows/ci.yml/badge.svg)](https://github.com/neiromaster/enver/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/neiromaster/enver.svg)](https://pkg.go.dev/github.com/neiromaster/enver)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)

Inject environment variables from named, layered YAML profiles into any child
command — **without mutating the target tool's own config**. Built for running
[Claude Code](https://github.com/anthropics/claude-code) against different
API keys / base URLs / model sets, but works for any command.

```
enverx anth -- claude
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
`enverx <profile> -- <command>` invocation that injects env only into the child
process.

## Install

**Homebrew** (macOS & Linux):

```sh
brew tap neiromaster/enver
brew trust neiromaster/enver   # Homebrew requires trusting third-party taps
brew install enver
```

The `enver` cask installs **both** the `enver` and `enverx` binaries and wires
up shell completions for both (bash, zsh, fish) automatically. On macOS the
cask also strips Gatekeeper quarantine, so the binaries run without a manual
"Allow Anyway" step.

**Go** (anywhere with a Go toolchain):

```sh
go install github.com/neiromaster/enver/cmd/enver@latest
go install github.com/neiromaster/enver/cmd/enverx@latest
```

This drops the `enver` and `enverx` binaries into `$GOBIN` (on your `PATH`). Or
build from source without installing:

```sh
git clone https://github.com/neiromaster/enver && cd enver && make build   # → ./bin/enver
```

`make build` produces `enver`; build the runner separately with
`go build ./cmd/enverx`. Pre-compiled binaries for linux/darwin/windows ×
amd64/arm64 are on the [releases page](https://github.com/neiromaster/enver/releases);
each archive includes both `enver` and `enverx`.

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
      ANTHROPIC_API_KEY: enc:v2:...
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
profile's own env first and confirms when it would remove keys (decline to abort;
`--force` skips the prompt; a non-interactive pipe without `--force` errors).
`--extends <profile>` sets or overrides `extends` (otherwise it is preserved,
including across `--replace`); an empty import without `--extends` is refused.
The summary prints a masked diff of what changed: `+` added, `~` overridden,
`-` removed (secret-looking values masked as in `enver show`).

> `dotenv -o` and `import` move decrypted secrets through a plaintext `.env`
> file. Mind where it lands; keep values at rest encrypted with `enver encrypt`.

## Encrypting secrets

Secrets in the config can be encrypted at rest so the config is safe to commit
to a dotfiles repo. Only individual secret-looking values are encrypted — keys,
structure and non-secret values (base URLs, model names) stay plaintext.

```sh
enver keygen                    # prompt for a passphrase, write the key cache (mode 0600)
enver keygen --random           # non-interactive: write a raw random key instead (for CI)
enver encrypt                   # encrypt secret-looking values in the config
enver encrypt glm --all         # encrypt every value in the "glm" profile
enver decrypt                   # restore plaintext (for editing)
```

`enver keygen` prompts for a passphrase twice and writes a key cache to
`~/.config/enver/key` (JSON, mode `0600`). The same passphrase always derives
the same key (argon2id; the salt comes from your encrypted values), so the key
can be regenerated from memory on any machine. `enver keygen --random` keeps the
legacy behavior of writing a raw random key, for CI and other non-interactive
setups.

`enver keygen --force` refuses to overwrite silently: when the configs contain
encrypted values and the new key differs from the current one, the interactive
path asks for confirmation and `--random` prints a warning, since an overwritten
key makes the existing ciphertext unreadable. Run `enver decrypt` with the old
key first if you need to migrate them.

New values are written as `enc:v2:<base64(salt||nonce||ciphertext)>` (AES-256-GCM
with the 16-byte argon2id salt embedded). Legacy `enc:v1:<base64(nonce||ciphertext)>`
values still decrypt, and are written when only a raw `--key`/`ENVER_KEY` key is
available. Encryption is idempotent — re-running `encrypt` skips already
encrypted values.

At runtime `enverx <profile> -- <command>` **transparently decrypts** with no
prompt, so the day-to-day command is unchanged. The key is resolved in this
order: `--key <path>` flag, `ENVER_KEY` env var (base64, for CI), the default
key file, then an interactive passphrase prompt that derives, verifies, and
caches the key. Where stdin is not a terminal the prompt is skipped and the
command fails loudly instead of hanging. A profile with no encrypted values runs
without any key.

**Recovery on a new machine:** clone the encrypted config, then run
`enver x <profile> -- <command>` (or `enver encrypt` / `enver decrypt`) and
enter the passphrase when prompted — the key is derived from the salt embedded
in your values, verified against them, and cached to `~/.config/enver/key`. From
then on the standalone `enverx` runner works as usual.

> Commit the encrypted config; never commit the key file. Encryption protects
> against accidental leaks (git, dotfiles, casual disk access), not against an
> attacker with read access to both the config and the key on the same machine.

## Usage

```
enverx [profile] -- <command> [args...]   Run command with the profile's env (dedicated runner)
enver x [profile] -- <command> [args...]  Same, inside enver (enverx is the detached form)
enver show [profile] [--no-mask] [--format text|json]  Preview resolved env (masked by default)
enver export [profile] [--format bash|powershell]      Print `export K=V` (unmasked, for eval)
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
enver keygen [--random] [--force]         Passphrase-derived key; --random for a raw key (CI)
enver encrypt [profile] [--all]           Encrypt secret values in the config
enver decrypt [profile]                   Decrypt values back to plaintext
enver --global / -g                       Write to the global config (default: ./.enver.yaml)
enver --config <path>                     Override the global config file (read path; --global writes)
enver --key <path>                        Key file (or ENVER_KEY env)
enver --no-local                          Ignore the ./.enver.yaml layer when reading
enver --no-expand                         Do not expand $VAR references
enver --chdir <dir>                       Run as if started from <dir> (.enver.yaml and relative --config resolve against it)
enver --version / enverx --version / -h, --help
```

> **Breaking:** the bare forms `enver <profile> -- <command>` and `enver <profile>`
> (preview) were removed. Use `enverx <profile> -- <command>` (or `enver x ...`)
> to run, and `enver show <profile>` to preview. `enver run` was renamed to `enver x`.

> **Breaking:** `enver init` was renamed to `enver add` — `init` implied initialization, but the command only adds a profile.

With no profile, the config's `default` is used. `enver show <profile>` previews
the resolved env (masked by default); `enver list` lists profiles.

The first positional token is matched against subcommand names (`x`, `show`,
`export`, `dotenv`, `import`, `list`, `add`, `edit`, `remove`, `rename`,
`duplicate`, `default`, `validate`, `keygen`, `encrypt`, `decrypt`,
`completion`) before being treated
as a profile, so a profile that shares one of those names must be run via the
explicit verb: `enverx <profile> -- <command>` (or `enver x ...`).

Secret-looking values (keys matching `key|token|secret|password|auth|credential`,
case-insensitive, or values that embed credentials in a URL such as
`postgres://user:pass@host`) are masked in `enver show` output (use `--no-mask`
to reveal) and encrypted by `enver encrypt`. Plain URLs without credentials
(`https://api.example.com`) are left alone. `enver export` is always unmasked so
`eval "$(enver export <profile>)"` applies to the current shell. `--format
powershell` emits `$env:K = 'V'` for PowerShell (`enver export <profile>
--format powershell | iex`); `bash` is the default. `show`/`list` also accept
`--format json` for machine-readable output (`show` still honors `--no-mask`).

## How it works

1. Load global config, then overlay each `.enver.yaml` from home down to cwd.
2. Resolve the selected profile (or `default`), walking `extends` with cycle
   detection — root applied first, child overrides parent.
3. For `enver x` / `enverx`: child env = current `os.Environ()` ⊕ profile env,
   then `exec` the command with stdin/out/err connected and the child's exit code
   propagated. On a terminal, enver first primes the tab title (VS Code agent
   mode + profile name) so the child's own title shows live. Windows has no
   `exec`, so there the child is spawned and waited on instead.

No file under `~/.claude/` or elsewhere is modified.

## Security

- **Secrets at rest** — `enver encrypt` stores values as `enc:v2:` ciphertext
  (AES-256-GCM, argon2id salt embedded); legacy `enc:v1:` values still decrypt.
  The key cache lives at `~/.config/enver/key` (mode `0600`) —
  **never commit the key**.
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
auto-formats staged Go files. Enable it once after cloning — lefthook is pinned
as a Go tool dependency in `go.mod`, so nothing global is required:

```sh
make hooks   # runs `go tool lefthook install`
```

CI also runs `gofmt` and `golangci-lint`, so unformatted code won't merge.

Commits follow [Conventional Commits](https://www.conventionalcommits.org/).

## License

MIT — see [LICENSE](./LICENSE).