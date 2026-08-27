# Contributing

enver is a thin, domain-agnostic shim by design — features that encode
knowledge of the launched command (e.g. API health probes) belong elsewhere.
Bug reports, docs and tests are welcome.

## Local checks

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
