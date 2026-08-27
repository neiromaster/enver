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

## Two layers

enver uses exactly two layers: the global config above plus a local
`./.enver.yaml` in the current directory (no walk-up — only that one file).
When both define the same profile, env keys overlay per-key (local wins) and
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
keys are overridden per-key; when both layers carry a profile's `unset` list it
stays the overriding layer's verbatim rather than merging; `extends` lists merge
as `[global…, local…]`, so a local parent takes priority over a global parent on
a shared key.

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
(`[A-Za-z_][A-Za-z0-9_]*`, the same rule `.env` import applies): names ride
unquoted inside eval'd export lines, so enver refuses anything looser at config
load, at every write boundary, and at TUI input time.
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
