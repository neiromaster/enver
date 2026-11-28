# enver

[![CI](https://github.com/neiromaster/enver/actions/workflows/ci.yml/badge.svg)](https://github.com/neiromaster/enver/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/neiromaster/enver.svg)](https://pkg.go.dev/github.com/neiromaster/enver)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)

**One YAML store. Many environments. Your tool's own config stays untouched.**

Inject environment variables from named, layered YAML profiles into any child
command — without mutating the target tool's own configuration. Built for
running [Claude Code](https://github.com/anthropics/claude-code) against
different API keys / base URLs / model sets, but works for any command: enver
is a provider-agnostic exec shim that speaks plain YAML and plain env vars.

```console
$ enver show dev
# profile: dev → base-anthropic → opus-models
ANTHROPIC_API_KEY=sk-a…(len=27)  # from base-anthropic (global) ~ marked, not encrypted
ANTHROPIC_BASE_URL=https://api.anthropic.com  # from base-anthropic (global)
ANTHROPIC_MODEL=claude-opus-5  # from opus-models (global)
ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-4-5-20251001  # from dev (local)

$ enver x dev -- claude          # all of it injected into this child only
$ enver x dev offline -- claude  # compose profiles: later one wins shared keys

$ enver export offline           # fencing drops keys instead of redefining them
export ANTHROPIC_BASE_URL='https://api.anthropic.com'
```

One global store at `~/.config/enver/config.yaml`, one optional project file
at `./.enver.yaml`, zero mutation of `~/.claude/` or anything else under your
tools' control.

## Install

**Homebrew** (macOS & Linux):

```sh
brew tap neiromaster/tap
brew trust neiromaster/tap       # required for third-party taps
brew install enver               # wires shell completions; strips Gatekeeper quarantine on macOS
```

`brew uninstall enver` keeps your data; `brew uninstall --zap enver` also
trashes `~/.config/enver` — including the encryption key, which then comes
back only from `enver keygen` with your passphrase. Your data is spared only
if `XDG_CONFIG_HOME` is an absolute path other than `~/.config` — then it
lives there and zap does not follow it.

**Go** (anywhere with a Go toolchain):

```sh
go install github.com/neiromaster/enver/cmd/enver@latest
```

**npm**:

```sh
npm install -g @enver-go/enver
```

**Script** (no package manager, macOS/Linux):

```sh
curl -fsSL https://raw.githubusercontent.com/neiromaster/enver/main/scripts/install.sh | bash
# pin a version:
curl -fsSL https://raw.githubusercontent.com/neiromaster/enver/main/scripts/install.sh | bash -s -- v0.9.1
```

Windows (PowerShell 5.0+):

```powershell
irm https://raw.githubusercontent.com/neiromaster/enver/main/scripts/install.ps1 | iex
# pin a version:
$env:ENVER_VERSION = "v0.9.1"; irm https://raw.githubusercontent.com/neiromaster/enver/main/scripts/install.ps1 | iex
```

On Windows the installer also dot-sources the bundled
`completions/enver.ps1` from your `$PROFILE`; `ENVER_NO_COMPLETION=1` opts
out. Under a `Restricted` execution policy completions will not load — the
installer prints the fix:

```powershell
Set-ExecutionPolicy -Scope CurrentUser RemoteSigned
```

> **Piping a script to your shell runs it without review — read it first.**
> The installer verifies the download against the release's SHA-256
> checksums, then installs to `~/.local/bin` (macOS/Linux) or
> `%LOCALAPPDATA%\enver\bin` (Windows); `ENVER_INSTALL_DIR` overrides.

Uninstall a script install — macOS/Linux, remove the binary; Windows, let the
installer undo what it added (binary, completion line, PATH entry):

```sh
# macOS/Linux
rm "$(command -v enver)"
```

```powershell
# Windows
$env:ENVER_UNINSTALL = "1"; irm https://raw.githubusercontent.com/neiromaster/enver/main/scripts/install.ps1 | iex
# or, on a script you saved first:
.\install.ps1 -Uninstall
```

Your user data lives in `~/.config/enver` — `config.yaml`, the encryption
key, pulled remotes — and survives the script uninstalls above; delete it
yourself if that is what you want. `brew uninstall --zap enver` removes it
along with the cask. With a custom `XDG_CONFIG_HOME`, see the Homebrew note
above.

**Update**:

```sh
enver update              # via the owning package manager, or self-replace
enver update --check      # exit 0 up to date, 1 update available, 2 error
```

`enver update` delegates to npm / brew / `go install` when enver was
installed that way; a directly-installed binary is replaced from GitHub
releases. The new binary takes effect on the next launch. Version lookups
use the GitHub API (60 requests/hour unauthenticated; set `GITHUB_TOKEN`
to raise the limit).

Build from source (`make build` → `./bin/enver`) or grab pre-compiled
linux/darwin/windows × amd64/arm64 archives with completions from the
[releases page](https://github.com/neiromaster/enver/releases).

## Why enver

`dotenvx` / `dotenv` load `.env` files local to the working directory;
`direnv` layers by directory but through a shell hook and per-dir files;
claude-specific switchers mutate `~/.claude/settings.json`. enver keeps one
store, inherits like code, and hands the result to a single child process.

| Feature | Doc |
|---|---|
| Layered configs: remote content ⊕ global store ⊕ project file, per-key overlay, no walk-up | [`config.md`](docs/config.md#layers) |
| Work on one config file as the whole config (`--file`), with the content pulled for it stored beside it in `.enver/<file>/` | [`config.md`](docs/config.md#working-on-one-file) |
| Check a config before publishing it: `enver validate --strict <file>`, from a pre-commit hook | [`remotes.md`](docs/remotes.md#validate) |
| Remote configs: `enver pull` a shared YAML, patch its profiles in place, keep working offline | [`remotes.md`](docs/remotes.md#remotes) |
| Multi-parent `extends`: mixins composed left-to-right, transitive inheritance | [`config.md`](docs/config.md#multiple-parents) |
| Run-line composition: `enver x base slot -- cmd` is an anonymous `extends` at launch | [`cli.md`](docs/cli.md#composing-profiles) |
| `unset` fences: keys enver must never set; shell values pass through untouched | [`config.md`](docs/config.md#suppressing-variables-unset) |
| Provenance: every value annotated `# from <profile> (<layer>)` in `show` | [`cli.md`](docs/cli.md#preview-and-masking) |
| Secrets encrypted at rest (argon2id + AES-256-GCM), transparent decrypt | [`secrets.md`](docs/secrets.md) |
| `~KEY` marks any variable a secret candidate, whatever its name | [`secrets.md`](docs/secrets.md#marking-arbitrary-values) |
| `.env` round-trip with comments, raw `$VAR` references, diff-marked import | [`dotenv.md`](docs/dotenv.md) |

## How it works

1. Load every pulled remote's stored content, overlay the global config, then
   the local `./.enver.yaml`.
2. Resolve the selected profiles — one, or several composed left to right —
   walking `extends` with cycle detection: root first, child overrides parent.
3. For `enver x`: child env = current environment ⊕ the composed profile env,
   then exec with stdio connected and the exit code propagated.

## Full manual

- [`docs/config.md`](docs/config.md) — layers, merge rules, `extends`,
  suppressing variables
- [`docs/remotes.md`](docs/remotes.md) — `enver pull`, nesting, trust,
  authoring a config to publish, `validate`
- [`docs/profiles.md`](docs/profiles.md) — `add`, `edit`, remove/rename/
  duplicate/default
- [`docs/cli.md`](docs/cli.md) — command and flag reference, masking,
  removed forms
- [`docs/dotenv.md`](docs/dotenv.md) — `.env` import/export
- [`docs/secrets.md`](docs/secrets.md) — encryption, key management, threat
  model
- [`docs/contributing.md`](docs/contributing.md) — local checks, hooks,
  conventions

MIT — see [LICENSE](./LICENSE).
