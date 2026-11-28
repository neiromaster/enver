# Managing profiles

Interactive creation and editing, plus the non-interactive management
commands. Config format rules live in [`config.md`](./config.md).

## Creating profiles interactively

`enver add` walks you through a new profile and writes it into the write
target — `./.enver.yaml` by default, the global config under `--global` —
preserving any existing structure and comments:

- **Profile name** — letters, digits, `-`, `_`; must start with a letter or
  digit. A bare name is your own; one `/` inside it names a remote's profile,
  `record/profile` (see [Remote-projected profiles](#remote-projected-profiles)).
  Pass it as an argument (`enver add glm`) to skip this prompt.
- **Extends** — one or more parents picked from a list of existing profiles,
  in inheritance order; reorder in place with `<`/`>` or `←`/`→`, confirm
  nothing picked for no `extends`. Skipped when no profiles exist yet. A pick
  that would form an `extends` cycle is rejected immediately; a chain value
  the picker cannot offer — a parent in the other layer, or a deleted one —
  shows up as a dimmed `(external)` row you keep or drop explicitly.
- **Variables** — for each, enter a name, a value, and an optional comment;
  leave the name blank to finish. A profile needs at least one variable or an
  extends.
- **Default** — optionally set as the default profile.

The optional comment is stored as a YAML comment above the entry. It survives
`enver encrypt`, so you can keep a plaintext hint (e.g. a link to the secret
store) above an encrypted value — see [`secrets.md`](./secrets.md):

```yaml
profiles:
  glm:
    env:
      # get this token from https://vault.example/anth
      ~ANTHROPIC_API_KEY: enc:v3:argon2id:...
```

Env keys merge additively into an existing profile of the same name.

Mutating commands (`add`, `edit`, `remove`, `rename`, `duplicate`, `default`,
`import`, `encrypt`, `decrypt`) write to `./.enver.yaml` by default; pass
`--global` (`-g`) to write the user config instead. `enver add` creates the
local file if it is absent. When authoring locally, the `extends` picker lists
the merged view — local and global profiles plus the projected profiles of every
pulled remote; under `--global` it lists global profiles only (a global profile
must not extend a local one, which would break outside this directory), so a
remote-provided parent shows up there as a dimmed `(external)` row.

Profile completion follows the same split: read commands (`show`, `export`,
`dotenv`, `x`) complete against the merged view — both user layers plus every
remote-projected name — while mutating commands complete the profiles the write
target declares. `add` and `edit` are the exception: they also offer the
remote-projected names they can patch into the target. On a read command every
position before `--` offers profiles (already-typed members drop out of the
candidates), so completion keeps working at every step of a composed run line.

## Editing profiles

`enver edit` opens a profile in an interactive menu where the profile's own
variables are listed and editable, while variables inherited through `extends`
are shown read-only (marked `(inherited)`) — change those in the profile they
come from. Nothing is written until you select **Done**, so you can experiment
freely. Press **ESC** at any time to cancel the current prompt and return to
the menu:

- **Add or edit a variable** — select an existing var to change its value or
  comment, or add a new one (name, value, optional comment, as in `add`).
- **Change extends** — pick one or more parents; the selection order is the
  inheritance order and can be rearranged in place (`<`/`>`, `←`/`→`).
  Confirming with nothing picked clears `extends`, a choice that would form
  an `extends` cycle is rejected when you commit, and chain values the
  picker cannot offer (other layer, deleted) show as dimmed `(external)`
  rows you keep or drop explicitly.
- **Toggle default** — set or clear this profile as the default.
- **Secrets** — lists the profile's own variables (its marks open checked)
  plus the inherited ones it can take over. Selecting a parent's key
  materializes it as an own entry with its value, comment, and mark, and a mark
  is cleared only here: a plain name carries no information, so typing the key
  without `~` keeps the mark. Marks are staged like every other edit — they
  land on **Done**, and Cancel discards them — and marked rows show their `~`.
  An own encrypted value is masked in its row (and decrypted first when a key
  resolves), while an inherited row carries no decryption state, so it may show
  the stored ciphertext. One row, **Encrypt now**, schedules the encrypt step to
  run once the commit has written the file; without a key it notes the
  plaintext and leaves the marks in place. The lifecycle and the local-override
  idiom live in
  [`secrets.md`](./secrets.md#marking-arbitrary-values).
- **Delete variable** — remove one or more of the profile's own variables.
  Keys whose fence would outlive them carry a `· unset` mark on their row, and
  deleting one says so before you leave the screen.
- **Manage unsets** — choose which keys this profile must never set. Declared
  fences open pre-checked at the top (`⊘ … (declared here)`); remaining own
  and inherited keys are candidates below. Inside the picker,
  `space`/`-`/`x` toggle. Declaring a fence shows in the menu immediately:
  a fenced key keeps its row but fades to dim — own rows and inherited ones
  alike stay listed, just visibly muted instead of active, while the value it
  no longer resolves to stays hidden from everywhere else (`show`, `x`,
  export). Fencing a key the profile also defines asks for confirmation —
  declining drops just that fence, not the rest of the pass — and so does
  adding or renaming a variable onto an already-fenced key, where declining
  lifts the fence instead. Confirming with nothing checked removes every
  declared fence, behind its own confirmation. Leaving mid-edit offers to
  resume the toggles rather than discarding them. A fence outlives the
  variable it names — delete both separately if that is what you mean.
- **Delete profile** — remove the whole profile. It is refused while other
  profiles extend it; if the profile is the default, the default is cleared
  along with it (see `remove` below).

Cancelling exits without writing. A profile must keep at least one variable,
an `extends`, or an `unset`, so **Done** is rejected on an empty profile.

## Remote-projected profiles

A pulled remote contributes profiles under its record prefix (`llm/ollama`,
`llm/qwen`) without living in any file you edit: remote content is a read-only
base below your own layers. The rules that follow from that, and the commands
that carry them, are in [`remotes.md`](./remotes.md).

- **Patch with `add` or `edit`.** On a name only remote content provides, both
  open on the remote's keys: they are shown as inherited rows, the entries you
  make override them per key, and the save writes only your entries into the
  write target — the patch the feature exists for. The closing summary says
  which remote it patches.
- **Remote content is never a write target.** The other mutators (`remove`,
  `rename`, `duplicate`, `default`, `encrypt`, `decrypt`) refuse a remote-only
  name and point at the patch copy; deleting remote content is
  `enver pull --rm <record>`.
- **Patches in the other user layer keep the scope hint.** When the other user
  layer already declares the name, `add` refuses with the usual scope hint
  (re-run with `-g`, or without it) rather than writing a second patch that
  would shadow your own per key — a patch is written in the layer that owns it.
- **A prefixed name is addressed in full.** There is no bare-name matching:
  `enver x llm/qwen` runs it, and `llm/qwen` is what the pickers and completion
  offer.

Pulled content is replaced wholesale on every re-pull, so a patch is the only
way to make a remote profile yours: never hand-edit the stored file — it lives
in `$XDG_CONFIG_HOME/enver/remotes/` and the next pull overwrites it.

## remove · rename · duplicate · default

- **`enver remove [profile]`** — delete a profile. Refused while other
  profiles extend it (the error names the dependents); repoint or remove those
  first. If the profile is the default, the default is cleared with it. Pass
  `--yes` / `-y` to skip the confirmation prompt.
- **`enver rename [old] [new]`** — rename a profile and rewrite every
  reference in the same file: any `extends: old` in other profiles and the
  top-level `default` if it matches. Refused if the new name already exists.
  (References in the *other* layer are not rewritten; `validate` surfaces any
  that go dangling.)
- **`enver duplicate <src> [new]`** — make a structural copy of a profile
  (extends, env, and comments). The copy is not made the default.
- **`enver default [profile]`** — with no argument, print the current default;
  with a name, set it; `--clear` removes the default. The effective default is
  the local file's `default` when set, otherwise the global file's — a project
  can pin its own default while keeping the user-level one as fallback.
