# enver

**One YAML store. Many environments. Your tool's own config stays untouched.**

`enver` injects environment variables from named, layered YAML profiles into
any child command — without mutating the target tool's own configuration.

```console
$ enver show dev
ANTHROPIC_API_KEY=sk-a…(len=27)  # from base-anthropic (global)
$ enver x dev -- claude
```

This package installs the prebuilt `enver` binary for your platform
(linux/darwin/windows × amd64/arm64). No build step, no install scripts.

## Install

```sh
npm install -g @neiromaster/enver
# or run without installing:
npx @neiromaster/enver --help
```

## Docs

Full manual: <https://github.com/neiromaster/enver#readme>
