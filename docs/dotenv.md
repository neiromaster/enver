# `.env` import and export

`enver dotenv` writes a profile as a standard `.env` file — values decrypted
and unmasked, per-key comments preserved — and `enver import` reads one back
into a profile. They round-trip: hand a profile to any tool that expects
`.env`, then bring the result back.

```sh
enver dotenv prod -o prod.env          # export profile prod to prod.env
enver import prod.env staging          # import into a new profile (merged)
enver import prod.env prod --replace   # reset prod to exactly prod.env (confirms)
```

Imported values are stored **raw** — `$VAR` references stay as templates, not
expanded — so layered profiles survive the round-trip. `import` **merges** by
default (imported keys override existing same-named keys); `--replace` wipes
the profile's own env and `unset` list first, confirming before it removes
keys or clears unset entries (decline to abort; `--force` skips the prompt;
non-interactive pipes without `--force` error out).
`--extends <profile>` sets or overrides `extends` (otherwise it is preserved,
including across `--replace`); an empty import without `--extends` is refused.
The summary prints a diff of what changed: `+` added, `~` overridden,
`-` removed (values shown in full — the data came from your own .env file).
Keys the profile's own `unset` list fences are marked `!` — written, though
they never reach `show`, `export`, or `enver x`.

> `dotenv -o` and `import` move decrypted secrets through a plaintext `.env`
> file. Mind where it lands; keep values at rest encrypted with `enver
> encrypt`.
