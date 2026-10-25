# Contributing

enver is a thin, domain-agnostic shim by design — features that encode
knowledge of the launched command (e.g. API health probes) belong elsewhere.
Bug reports, docs and tests are welcome.

## Local checks

```sh
make vet lint test   # fast feedback
make cover-gate      # the coverage gate CI enforces (MIN_COVER=78)
make shim            # npm shim tests (node --test)
```

A pre-commit hook (via [lefthook](https://github.com/evilmartians/lefthook))
auto-formats staged Go files and runs golangci-lint. Enable it once after
cloning — lefthook and
golangci-lint are pinned as Go tool dependencies in the isolated `tools/`
module (reached through `go.work`), so nothing global is required:

```sh
make hooks   # runs `go tool lefthook install`
```

Keep `tools/` free of .go files: the tools module must stay invisible to
`go build`/`go test`/`go vet` run from the repo root.

The `use` order in `go.work` matters too: the repo root must stay first so
release tooling treats it as the main module.

`completions/` is generated (`make completions`) and committed: CI fails when
a cobra or command-tree change would alter it.

CI also runs `gofmt`, `golangci-lint`, and `go mod tidy` checks, so
unformatted or untidy code won't merge.

Commits follow [Conventional Commits](https://www.conventionalcommits.org/).

## Releasing

Before tagging, cut the changelog: rename the `Unreleased` section to the
new version with its date and start a fresh empty `Unreleased` above it —
the section is never cut automatically. Then tag `vX.Y.Z` and push the tag;
the Release workflow runs the tests first, then publishes the GitHub
release, the Homebrew cask, and the npm packages.
