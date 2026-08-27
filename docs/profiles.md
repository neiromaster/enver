# Managing profiles

Interactive creation and editing, plus the non-interactive management
commands. Config format rules live in [`config.md`](./config.md).

## Creating profiles interactively

`enver add` walks you through a new profile and writes it into the global
config, preserving any existing structure and comments:

- **Profile name** — letters, digits, `-`, `_`; must start with a letter or
  digit. Pass it as an argument (`enver add glm`) to skip this prompt.
- **Extends** — picked from a list of existing profiles (with a `(none)`
  option). Skipped when no profiles exist yet.
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
      ANTHROPIC_API_KEY: enc:v3:argon2id:...
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
  Keys whose fence would outlive them carry a `· unset` mark on their row, and
  deleting one says so before you leave the screen.
- **Manage unsets** — choose which keys this profile must never set. Declared
  fences open pre-checked at the top (`⊘ … (declared here)`); remaining own
  and inherited keys are candidates below. Inside the picker,
  `space`/`-`/`x` toggle. Declaring a fence takes effect in the menu right
  away: the profile's own row grows a `· unset` mark (`⊘`), and a fenced
  inherited key disappears from the inherited view since it no longer
  resolves. Fencing a key the profile also defines asks for confirmation —
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

## validate

`enver validate` audits config health: dangling `extends` and `extends`
cycles (errors); profiles with nothing to contribute — no env, no extends, no
unset (warning); and a profile that both defines and unsets the same key in
one layer (`contradictory-unset`, warning). It also checks the global file in
isolation and flags a global profile that extends a local-only name (fine in
this directory, broken elsewhere). Exits non-zero if any error is found.
