# crocs — project rules

## No compatibility. Ever.

crocs has one user and no installed base to protect. Do not write
migration code, compatibility shims, fallbacks for "old rows", deprecated
aliases, or version-detection branches.

- **Schema changes replace, they never migrate.** Bump `SchemaVersion`,
  rewrite the `schema` constant, done. A registry at any other version is
  dropped and recreated on open; clones on disk stay and get re-fetched.
- **Wire format changes just change.** Rename or remove JSON fields freely.
  Nobody needs the old shape; the skill and README are updated in the same
  change.
- **CLI changes just change.** Rename commands and flags, drop old forms.
  No aliases kept "for a while".
- **Delete the old thing completely.** A replaced design leaves no code,
  comments, tests, or docs behind that describe the previous behaviour.

If you catch yourself writing "for backward compatibility", "legacy",
"pre-vN", or "still accept the old …" — stop and remove it.

## Style

- Breaking changes are the default. Design for the right solution, not the
  one that preserves what exists.
- Test with throwaway prototypes in the scratchpad, then discard them
  entirely.
- `make build` stages `skills/crocs/SKILL.md` into `internal/skill/`; edit
  only the canonical copy under `skills/`.
