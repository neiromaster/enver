# enver

[![CI](https://github.com/neiromaster/enver/actions/workflows/ci.yml/badge.svg)](https://github.com/neiromaster/enver/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/neiromaster/enver.svg)](https://pkg.go.dev/github.com/neiromaster/enver)
[![license](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)

Inject environment variables from named, layered YAML profiles into any child
command — **without mutating the target tool's own config**. Built for running
[Claude Code](https://github.com/anthropics/claude-code) against different
API keys / base URLs / model sets, but works for any command.

```
enver run anth -- claude
enver run openrouter -- claude --model claude-sonnet-5
eval "$(enver prod-db --export)"
enver init                  # interactively create a profile
enver encrypt               # encrypt secret values at rest
```

## Why

`dotenvx` / `dotenv` load `.env` files local to the working directory. `direnv`
layers by directory but via a shell hook and per-dir files. Existing
claude-specific switchers (`ccm`, `claude-code-switcher`) mutate
`~/.claude/settings.json` or hardcode the `ANTHROPIC_*` schema.

`enver` is a **provider-agnostic exec shim**: one YAML store, profiles that can
inherit via `extends`, layered `cwd → home`, and a clean
`run <profile> -- <command>` invocation that injects env only into the child
process.

## Install

**Homebrew** (macOS & Linux):

```sh
brew tap neiromaster/enver
brew trust neiromaster/enver   # Homebrew requires trusting third-party taps
brew install enver
```

**Go** (anywhere with a Go toolchain):

```sh
go install github.com/neiromaster/enver/cmd/enver@latest
```

This drops the `enver` binary into `$GOBIN` (on your `PATH`). Or build from
source without installing:

```sh
git clone https://github.com/neiromaster/enver && cd enver && make build   # → ./bin/enver
```

Pre-compiled binaries for linux/darwin/windows × amd64/arm64 are on the
[releases page](https://github.com/neiromaster/enver/releases).

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

`enver` walks from the current directory up to (but not including) `$HOME`,
looking for `.enver.yaml` files. They are merged **home-side first, cwd last**,
so the closest `.enver.yaml` wins. Use this for project-local overrides without
touching the global store:

```yaml
# ./.enver.yaml — pin a heavier model for this repo only
profiles:
  anth:
    env:
      ANTHROPIC_MODEL: claude-opus-5
```

Merge rules: `default` is overridden when set; profiles union; per-profile env
keys are overridden per-key; `extends` is taken from the closer layer when set.

## Creating profiles interactively

`enver init` walks you through a new profile and writes it into the global
config, preserving any existing structure and comments:

```
$ enver init
Profile name: glm
Extends (blank for none) (available: anth): anth
Environment variables (KEY=value, blank line to finish):
  ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic
  ANTHROPIC_API_KEY=sk-...
  ANTHROPIC_MODEL=glm-5

Set "glm" as the default? (current default: anth) [y/N] y
✓ wrote profile "glm" to ~/.config/enver/config.yaml
```

Pass the name as an argument to skip the first prompt: `enver init glm`. Env
keys merge additively into an existing profile of the same name.

## Encrypting secrets

Secrets in the config can be encrypted at rest so the config is safe to commit
to a dotfiles repo. Only individual secret-looking values are encrypted — keys,
structure and non-secret values (base URLs, model names) stay plaintext.

```sh
enver keygen                    # create ~/.config/enver/key (mode 0600)
enver encrypt                   # encrypt secret-looking values in the config
enver encrypt glm --all         # encrypt every value in the "glm" profile
enver decrypt                   # restore plaintext (for editing)
```

Encrypted values use the format `enc:v1:<base64(nonce||ciphertext||tag)>`
(AES-256-GCM). Encryption is idempotent — re-running `encrypt` skips already
encrypted values.

At runtime `enver <profile> -- <command>` **transparently decrypts** with no
prompt, so the day-to-day command is unchanged. The key is resolved in this
order: `--key <path>` flag, `ENVER_KEY` env var (base64, for CI), then the
default key file. A profile with no encrypted values runs without any key.

> Commit the encrypted config; never commit the key file. Encryption protects
> against accidental leaks (git, dotfiles, casual disk access), not against an
> attacker with read access to both the config and the key on the same machine.

## Usage

```
enver run [profile] -- <command> [args...]  Run command with the profile's env (canonical)
enver [profile] -- <command> [args...]      Shorthand for the above
enver [profile]                             Preview resolved env (secrets masked)
enver [profile] --print                     Same, explicit
enver [profile] --export                    Print `export K=V` (unmasked, for eval)
enver init [name]                           Interactively create a profile
enver keygen [--force]                      Generate the encryption key file
enver encrypt [profile] [--all]             Encrypt secret values in the config
enver decrypt [profile]                     Decrypt values back to plaintext
enver -l, --list                            List profiles
enver --config <path>                       Override global config file
enver --key <path>                          Key file (or ENVER_KEY env)
enver --no-local                            Ignore .enver.yaml layers
enver --no-mask                             Show full secrets in --print
enver -v, --version
enver -h, --help
```

With no profile, the config's `default` is used. A bare `enver <profile>`
previews the resolved env; a bare `enver` lists profiles.

The first positional token is matched against subcommand names (`run`, `init`,
`keygen`, `encrypt`, `decrypt`, `completion`) before being treated as a profile,
so a profile that shares one of those names must be run via the explicit verb:
`enver run <profile> -- <command>`.

Secret-looking values (keys matching `key|token|secret|password|auth|credential`,
case-insensitive) are masked in preview output. `--export` is always unmasked so
`eval "$(enver <profile> --export)"` applies to the current shell.

## How it works

1. Load global config, then overlay each `.enver.yaml` from home down to cwd.
2. Resolve the selected profile (or `default`), walking `extends` with cycle
   detection — root applied first, child overrides parent.
3. For run mode: child env = current `os.Environ()` ⊕ profile env, then `exec`
   the command with stdin/out/err connected and the child's exit code propagated.

No file under `~/.claude/` or elsewhere is modified.

## Security

- **Secrets at rest** — `enver encrypt` stores values as `enc:v1:` ciphertext
  (AES-256-GCM) so the config is safe to commit. The key lives at
  `~/.config/enver/key` (mode `0600`) — **never commit the key**.
- **Preview masking** — `--print` redacts `key|token|secret|password|auth|credential`
  values; use `--no-mask` or `--export` to reveal them.
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