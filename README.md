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
ANTHROPIC_API_KEY=sk-a…(len=27)  # from base-anthropic (global)
ANTHROPIC_BASE_URL=https://api.anthropic.com  # from base-anthropic (global)
ANTHROPIC_MODEL=claude-opus-5  # from opus-models (global)
ANTHROPIC_SMALL_FAST_MODEL=claude-haiku-4-5-20251001  # from dev (local)

$ enver x dev -- claude          # all of it injected into this child only

$ enver export offline           # fencing drops keys instead of redefining them
export ANTHROPIC_BASE_URL='https://api.anthropic.com'
```

One global store at `~/.config/enver/config.yaml`, one optional project file
at `./.enver.yaml`, zero mutation of `~/.claude/` or anything else under your
tools' control.

## Install

**Homebrew** (macOS & Linux):

```sh
brew tap neiromaster/enver
brew trust neiromaster/enver     # required for third-party taps
brew install enver               # wires shell completions; strips Gatekeeper quarantine on macOS
```

**Go** (anywhere with a Go toolchain):

```sh
go install github.com/neiromaster/enver/cmd/enver@latest
```

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
| Two-layer configs: global store ⊕ project file, per-key overlay, no walk-up | [`config.md`](docs/config.md) |
| Multi-parent `extends`: mixins composed left-to-right, transitive inheritance | [`config.md`](docs/config.md#multiple-parents) |
| `unset` fences: keys enver must never set; shell values pass through untouched | [`config.md`](docs/config.md#suppressing-variables-unset) |
| Provenance: every value annotated `# from <profile> (<layer>)` in `show` | [`cli.md`](docs/cli.md#preview-and-masking) |
| Secrets encrypted at rest (argon2id + AES-256-GCM), transparent decrypt | [`secrets.md`](docs/secrets.md) |
| `.env` round-trip with comments, raw `$VAR` references, diff-marked import | [`dotenv.md`](docs/dotenv.md) |

## How it works

1. Load the global config, overlay the local `./.enver.yaml`.
2. Resolve the selected profile walking `extends` with cycle detection — root
   first, child overrides parent.
3. For `enver x`: child env = current environment ⊕ profile env, then exec
   with stdio connected and the exit code propagated.

## Full manual

- [`docs/config.md`](docs/config.md) — layers, merge rules, `extends`,
  suppressing variables
- [`docs/profiles.md`](docs/profiles.md) — `add`, `edit`, remove/rename/
  duplicate/default, `validate`
- [`docs/cli.md`](docs/cli.md) — command and flag reference, masking,
  removed forms
- [`docs/dotenv.md`](docs/dotenv.md) — `.env` import/export
- [`docs/secrets.md`](docs/secrets.md) — encryption, key management, threat
  model
- [`docs/contributing.md`](docs/contributing.md) — local checks, hooks,
  conventions

MIT — see [LICENSE](./LICENSE).
