# Config

Config formats and layering rules. For commands see [`cli.md`](./cli.md).

Global config lives at `$XDG_CONFIG_HOME/enver/config.yaml`
(default `~/.config/enver/config.yaml`). See
[`config.example.yaml`](../config.example.yaml) for a commented skeleton.

```yaml
default: anth

profiles:
  anth:
    env:
      ANTHROPIC_API_KEY: sk-ant-...
      ANTHROPIC_BASE_URL: https://api.anthropic.com
      ANTHROPIC_MODEL: claude-sonnet-5

  local-proxy:
    extends: anth                 # inherit anth's env, override below
    env:
      ANTHROPIC_BASE_URL: http://localhost:8082
```

## Layers

enver resolves against a fixed stack, lowest first:

1. **remote content** — the pristine file of every pulled remote, projected
   into that remote's namespace (see [Remotes](#remotes));
2. the **global** config above; and
3. the **local** `./.enver.yaml` in the current directory (no walk-up — only
   that one file).

The order holds no matter which layer declares a remote's record: a remote
never rises above your own config, so pulling one cannot silently change what a
hand-authored global profile resolves to.

Between the two user layers, env keys overlay per-key (local wins) and
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

Merge rules: `default` is overridden when set; an empty local `default:` never
clears the global default — clear it in the layer that set it with
`enver default --clear`; profiles union; per-profile env
keys are overridden per-key; when both layers carry a profile's `unset` list it
stays the overriding layer's verbatim rather than merging; `extends` lists merge
as `[global…, local…]`, so a local parent takes priority over a global parent on
a shared key.

## Remotes

A remote is a config file someone else publishes — typically in a GitHub repo —
that enver fetches on request and stores locally. Resolution never touches the
network: `show`, `x`, `list` and the TUI read the stored copy, and `enver pull`
is the only command that fetches.

Records live in the `remotes:` section of a config file, layer-scoped like
everything else:

```yaml
remotes:
  llm:
    url: github:someone/llm-profiles
    ref: v2
```

The record **name is the key** for everything: it prefixes the remote's
profiles, names the stored content file, and addresses the record in
`enver pull`. A name is single-level (`[A-Za-z0-9][A-Za-z0-9_-]*`, no `/`);
record names that differ only in case, or match a reserved Windows device name,
are refused because the name becomes a file name.

Records merge across layers by name: a local record replaces the global one
**whole** — URL, ref and pull mode — while a record no higher layer redeclares
survives. The URL is not the key, so the same source may be registered under
two names (one pinned, one tracking a branch) and the two namespaces never
interact. The fields are:

- `url` — the source (required); see below for the accepted forms.
- `ref` — optional; pins the source. A github ref becomes a path segment of the
  resolved URL, while on a plain https source it is a label (see below).
- `no_prefix` — optional; the pull mode the content was stored under, `true`
  when `enver pull --no-prefix` registered it.

### Sources

- `github:owner/repo[/path][@ref]` — one file in a public GitHub repo. `path`
  defaults to `enver.yaml` at the repo root, `ref` to `HEAD`, which
  raw.githubusercontent.com resolves to the default branch without an API call.
- `gist:owner/id[/file][@revision]` — one file in a GitHub Gist; `file` defaults
  to `enver.yaml`. A gist has no branches, tags or HEAD, so `revision` must be
  the full 40-character revision sha. A secret gist works the same way: the raw
  file is fetched anonymously, and anyone holding the id can read it.
- `https://…` — stored and fetched exactly as written. A trailing `@ref` is
  split into the record's `ref` at registration, but on a plain https source
  that ref is **label-only metadata**: it is compared by the divergence check
  and reported by pull, never appended to the URL — what such a source serves
  is whatever the address serves. A gist raw URL works as an ordinary https
  source and gets no revision tracking — the `gist:` form is what records the
  revision a pull served.
- `http://` is rejected, and so is every other scheme: enver fetches sources
  over https only.

A ref takes letters, digits, `.`, `_`, `+`, `~`, `-`. One grammar covers all
three forms — a github ref and a gist revision become path segments, and a plain
https ref travels into sidecar state and warnings — so a ref containing a slash
is rejected in every spelling. Pin a branch whose name has one by its commit
instead (`github:owner/repo@1f0c9ab`). A gist ref is a revision sha, not a
branch: an abbreviated or uppercase sha is refused at parse time, since the raw
endpoint resolves only the full 40-character lowercase sha.

### Prefix projection

At load, each remote's profiles are renamed `<name>/<profile>`: `llm` publishes
`ollama` and `qwen`, you address `llm/ollama` and `llm/qwen`. The rename happens
in memory, so the stored file stays byte-identical to what the source serves —
you can `diff` it against the URL and see exactly what you trust. A record whose
content was never fetched contributes no profiles, and nothing complains until
something names one: a reference to an unpulled remote's profile fails like any
missing profile, with the hint naming the `enver pull <name>` that supplies it
(see [`profiles.md`](./profiles.md#validate)). A record nothing references yet
is silent by design. (A declared child with no stored content is the exception
— see [Nested remotes](#nested-remotes).)

- An `extends` target resolves inside the file's own namespace: a bare name one
  of its profiles defines, or a `record/...` name under a record that file
  itself declares. Everything else — your profiles, another record's
  projection, the name the file was pulled under — is out of namespace, and a
  target outside it is refused at pull and errors at load, naming the file and
  the target. A base a file needs therefore travels with it: the author
  declares it in the file's own `remotes:` section (see
  [Nested remotes](#nested-remotes)). Composition across the boundary is yours
  to write: a local profile may extend a projected name, and a local profile
  with the same name as a projected one patches it per key — your keys win,
  the remote's other keys survive.
- `unset` entries are env keys, not profile names — never rewritten.
- Secret marks (`~KEY`) ride through untouched, so a patch you write over a
  marked remote value stays masked and remains an encrypt candidate.
- A remote's `default:` is discarded: which profile is default is your decision,
  not the author's.
- A `remotes:` section inside remote content declares that file's own subtree:
  pull fetches each declared record under the pulling record's path, and
  projection drops the section, so the declarations never join your registry —
  your own `remotes:` section stays the only thing that registers a record you
  can address (see [Nested remotes](#nested-remotes)).
- Control characters are stripped from every remote-sourced name, value and
  comment — they reach terminals, error messages and completion candidates.

`enver pull <source> --as <name> --no-prefix` stores the record without a
prefix, so its profiles keep their bare upstream names. That is deliberate
blending, for a team-standard config meant to merge into your namespace: pull
warns about every upstream name that collides with a profile you already have,
on every pull, first or repeat. When two `--no-prefix` remotes define the same
name, the fold runs in sorted record order — the later record wins — and load
says so. A record's pull mode is stored in it, so re-pulling never re-asks.

### Nested remotes

A fetched file can declare its own `remotes:`, and one pull of it fetches the
whole tree. A provider config that declares the base it extends brings both:
its own `model` and the base's `default` land as `ollama/model` and
`ollama/claude/default`, with the `extends` between them rewritten to the
composed names. Pull gates every file it fetches, then checks the composed
tree before anything is written — a pull that fails anywhere leaves the
previous content, sidecars and record set exactly as they were.

Scoping is lexical. A file sees its own profiles, bare names, and the subtrees
of the records its own `remotes:` block declares — nothing else: not your
profiles, not its siblings, not the name it was pulled under. Prefixes compose
at boundaries and never leak inward: every file is written in its own
coordinates, and it stays correct no matter what it is pulled under or how
deep. A `no_prefix` record inside a tree removes just that boundary's segment:
a no-prefix `claude` under prefixed `ollama` lands its profiles directly under
`ollama/`, and at the top level it lands them bare.

The stored tree mirrors the record paths — `remotes/ollama.yaml` plus
`ollama.meta`, and beside them a directory `ollama/` holding
`ollama/claude.yaml` plus `claude.meta` when the record declares a child.
Each record has its own sidecar, so a child carries its own commit and pull
time, and provenance names the exact record (`remote:ollama/claude`). A nested
record is not registered anywhere: your config's `remotes:` section holds
top-level records only, and the CLI addresses only those — `enver pull ollama`
refreshes the whole subtree, `enver pull ollama/claude` is not a thing, and
`pull --rm ollama` removes the whole stored subtree with the record.

Renaming never touches a file. Pull a provider under a different name
(`--as my-ollama`) and the whole subtree moves wholesale —
`my-ollama/claude/default` — with only the prefix changed: prefixes are
applied in memory, and the stored bytes stay byte-identical to what the
sources serve.

Siblings are strangers, and nothing deduplicates. The same source under two
parents is two records, with separate pristine copies and separate freshness —
`ollama/claude` and `zai/claude` may sit at different revisions. A sibling
that needs the same base declares its own record.

A re-pull syncs the subtree to the parent's current declaration. Newly
declared children are fetched; a child whose declaration changed URL is
fetched fresh under the same name; children the declaration dropped have their
stored files deleted — they are reproducible cache, and each deletion is
listed in the pull output. A child whose stored content and sidecar still
match its declaration is left untouched. A declaration tree that loops back to
a source already being fetched is refused naming the chain of sources, and a
chain deeper than 16 records is refused loudly — both before anything is
written. Loading never touches the network either: a declared child whose
stored content is missing contributes no profiles and warns, naming the
`enver pull` that fetches the subtree again.

### Stored content, and what pull guarantees

- **Content** lives in `$XDG_CONFIG_HOME/enver/remotes/` at each record's
  path — `ollama.yaml`, and `ollama/claude.yaml` for a nested record — one
  file per record, replaced whole on every pull (temp file plus rename). It is
  config, not cache: XDG cache is disposable by convention, and deleting it
  must never silently change what `enver x` exports.
- **Provenance** lives in a machine-local sidecar, `remotes/<name>.meta`: the
  fetched URL, the resolved ref, the commit sha a fetch resolved, and when the
  pull ran. Recording it in the record would dirty a checked-in `.enver.yaml`
  with machine-local state.
- **Pull is the validation gate.** Before replacing anything it checks the
  fetched file: it must parse, carry valid env names and unsets, hold no
  in-file `extends` cycle, and contain only current-era `enc:v3` values whose
  KDF parameters parse within the caps (with a count cap on encrypted values).
  A refused upstream fails the pull and leaves the record, the content and the
  sidecar exactly as they were.
- **Divergence.** At load, the record's resolved source is compared with the
  sidecar, and the check recurses into the subtree: a nested record is judged
  against the declaration its parent carries. When they disagree — a
  hand-edited ref, a re-pointed record, a child whose sync was interrupted —
  the stored content still serves (resolution stays offline) but the command
  warns loudly on stderr, naming both sources and the `enver pull <name>` —
  the top-level record — that re-syncs the subtree. Serving stale content
  silently is the failure the sidecar exists to prevent.

### Pull, patch, remove

```
enver pull <source> --as <name>   register a source and fetch it now
enver pull <name>                 re-fetch one registered remote
enver pull                        re-fetch every registered remote
enver pull --list                 list records with their source and last pull
enver pull --rm <name>            remove the record (and its content)
```

Registration writes the **global** config, because a record is usually personal
machine setup; pass `--local` to write the project's `.enver.yaml` instead, so
the record travels with the repository. Registering globally a name the local
file already declares is refused with that same `--local` hint: the local record
would override the global one anyway. Re-registering a name prints the old and
new source; when user-layer patch profiles sit under that prefix, a changed
source or pull mode is a **takeover** — it changes what your credentials
compose with, so pull asks first (`--force` accepts without asking, and is the
only non-interactive way through). The same gate covers a re-fetch that replaces
stored content the sidecar says came from another source, which is how a cloned
project that re-declares one of your records is caught. Registering under
`--no-local` does not read the project file, so that takeover check never sees
the patch profiles a hidden local layer holds.

`--list` is read-only: it renders every declared record with its source, whether
it is pinned, and the commit and time of its last pull, saying so when a record
was never pulled. An unpinned record — `@HEAD`, an unpinned gist, or an https
source with no ref — shows the commit its content was actually fetched at, since
nothing else about it is fixed. An unpinned gist resolves the revision it serves
through the gist API before the fetch, so its recorded commit names the stored
bytes; with the API down or rate-limited the pull still succeeds and the commit
is left empty.

`--rm` removes the **closest declaration** in the merged view — the one that
controls the source right now — so a removal never fails on the wrong layer;
`-g`/`--local` target an explicit layer instead. The content file is deleted
only when no layer still declares the name, hidden ones included — a record
`--no-local` keeps out of view still needs its content; a surviving declaration
is reported with the source it now resolves to. Deleting the content takes the
record's whole stored subtree with it, since a nested record's files live under
its parent's directory. A removal also lists the user profiles under the
removed prefix: they survive as standalone profiles but lose every inherited
key.

Pulled content is never hand-edited. Changes live in your own layers, which are
always above:

```yaml
# ./.enver.yaml — patches the projected llm/ollama per key
profiles:
  llm/ollama:
    env:
      OLLAMA_API_KEY: sk-live
```

`enver add llm/ollama` is the bootstrap for this: on a name only remote content
provides it writes the patch into the write target instead of refusing, and its
summary says which remote it patches. The other mutators — `edit`, `remove`,
`rename`, `default`, `encrypt`, `decrypt` — point at that same `add` on a
remote-only name, since remote content is a read-only base, and removing remote
content is `enver pull --rm`, never `remove`. See
[`profiles.md`](./profiles.md#remote-projected-profiles) for the management
side.

Write marks fold over the layers *under* the target: for a local write that is
the global file and every pulled remote, and for a `--global` write only the
remotes — the local file never influences what a global write spells. A `~`
mark inherited from remote content through a patch therefore survives the
write, as an inherited mark from the other user layer does.

### Encrypted values in remote content

Publishing `enc:v3` values in a remote is supported, not an error: pull warns
listing the encrypted keys, since they decrypt only for the author's key. That
is the distribution case — the author's own machines read them without edits,
and everyone else **patches** those keys with plaintext values in their own
layer, inheriting the mark so the replacement stays masked and
encrypt-eligible.

When a key's winning value is ciphertext a remote supplied, the profile degrades
per key rather than failing: `show` reports it (`(cannot decrypt: wrong key
(remote))`, and an `undecryptable` map in `--format json`), while `x`, `export`
and `dotenv` refuse loudly, naming the keys and this patch path — a secret
silently missing from a child is worse than a refusal. A value your **own**
layers supply that the key cannot read stays the hard key-mismatch error it
always was. Patch with **plaintext** while foreign ciphertext remains, and
encrypt only after every foreign value is replaced: your ciphertext beside the
author's puts two salts in one profile, which breaks passphrase recovery. The
full story is in
[`secrets.md`](./secrets.md#ciphertext-someone-else-wrote).

### Trust

Pulling a profile means trusting the author's non-credential values to receive
your credentials: a remote controls hosts, endpoints and registry URLs —
everything your local secret attaches to. Masking, marks and layer order do
nothing about that.

- **Pin what matters.** An unpinned (`@HEAD`) record tracks the default branch,
  so future content is whatever the namespace holder serves — including after a
  repository transfer or an account rename. Pin a ref, and pin a commit sha
  (`@<sha>`) for anything secret-adjacent.
- **Watch provenance.** `show` marks every value a remote supplied with its
  `remote:<name>` layer, which is the fastest way to notice an endpoint that
  changed under you.
- **Same-name record takeover** is the sharp edge: a cloned project that
  re-declares your global record `llm` with its own URL now controls what
  content your global patches compose with. Pull refuses to do that quietly —
  it warns and asks before fetching into a record whose patches would now
  compose with different content.

A dangling `extends` is an error whoever authored it. Strict scoping removed
the authorship grading: a remote file can no longer name a parent outside its
own namespace, so a reference no profile defines is a broken config, not one
waiting to be completed — `enver validate` reports every dangling `extends`
as an error, yours and a remote's alike. The hint survives where a pull can
help: a parent whose record is declared but not yet pulled carries the
`enver pull <name>` that supplies it.

## Multiple parents

A profile can extend several parents to compose mixins — small,
single-purpose profiles combined into one:

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

**Authoring multiple parents:** `add` and `enver edit` compose the full list —
parents are picked in order and reordered in place (`<`/`>`, `←`/`→`);
confirming with none picked means no `extends` (in `edit`, it clears the
existing chain). Several can also be set by editing the YAML
(`extends: [a, b]`) or via `enver import <file> <name> --extends a,b`.

## Suppressing variables (`unset`)

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
(`[A-Za-z_][A-Za-z0-9_]*`), optionally preceded by one `~` marking the variable
a secret candidate (meaningless on an `unset` entry — see
[`secrets.md`](./secrets.md#marking-arbitrary-values)): names ride unquoted
inside eval'd export lines, so enver refuses anything looser at config load, at
every write boundary, and at TUI input time — the same rule `.env` import
applies; `.env` files never carry the mark.
`$VAR` interpolation sees the shell's live value through an unset: a key enver
does not set is simply absent from the resolved env, so a reference to it
expands to whatever the shell exports — matching what the child process gets.

Unsets are layered like everything else, but lists are never unioned across
copies of a profile: each copy applies its own unset at its turn — the global
file's fold first, the cwd file's second — so a key the later copy defines
beats an earlier copy's unset. Fences also keep working along extends chains:
one strips its target no matter which ancestor supplies the value, so an
unrelated local override of that profile cannot resurrect it. The reverse is
just as strict — a mention from a later layer always outruns an earlier fence;
redefine the key in the cwd copy, or in an ancestor refilled by that layer,
and the older fence goes silent from there on. Within one file the same rules
hold branch-by-branch: a profile's own unset strips what its ancestors
supplied, while a sibling parent listed alongside contributes fresh values
regardless of unsets inside the other parent's branch.

Author `unset` in YAML like multi-parent `extends` — a single name or a list,
never a mapping (the interactive `add`/`edit` menus do not manage the list
yet; edit YAML to change it). `enver validate` flags an `unset` naming a key
the same profile also sets in the same layer's env (`contradictory-unset`; a
cross-layer pair — say a local unset suppressing a global definition —
resolves by ordering instead and draws no warning). On Windows, where env
names are case-insensitive, unset matching is case-insensitive too.
