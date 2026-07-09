# crocs

A CLI for exploring git repositories with AI agents. Fetch a repo locally,
then query it with `grep`, `read-files`, `symbols`, `summary`, `tree`, and
`map` — fast enough to delegate the read-heavy work to disposable subagents
that return only the handful of files that matter.

The expensive part of code exploration is the *reading*. Skimming eighty
candidate files to find the eight that matter is a tax the main agent
shouldn't pay. crocs is the toolkit that makes the subagent delegation
loop cheap; the loop itself lives in the included skill.

## Install

```sh
go install github.com/<owner>/crocs/cmd/crocs@latest
crocs install-skill                # writes SKILL.md to ~/.claude/skills/crocs/
```

From source:

```sh
git clone https://github.com/<owner>/crocs
cd crocs
make install
```

A modern `git` on `$PATH` is recommended. crocs uses it for blobless +
shallow clones, an order of magnitude faster than the pure-Go fallback on
large repositories.

## Quick start

```sh
crocs fetch https://github.com/spf13/cobra.git
crocs summary cobra                # README excerpt + manifests + languages
crocs map cobra                    # directory file-count heatmap
crocs grep cobra "completion" -i completions.go -C 1
crocs read-files cobra completions.go --lines 200-300
crocs symbols cobra -p completions.go
crocs symbols --name Complete --kind function,method
```

## Commands

| Group | Commands |
|---|---|
| **Registry** | `fetch`, `list`, `info`, `path`, `remove` |
| **Orient** | `summary`, `tree`, `map`, `detect` |
| **Query** | `grep`, `read-files`, `symbols` |
| **VCS** | `branches`, `tags`, `log`, `diff`, `checkout`, `update`, `update-all`, `unshallow` |
| **Skill** | `install-skill` |

The default fetch is a shallow single-branch clone, so `branches`/`tags`/
`log` see only the fetched ref until `crocs unshallow <name>` — their JSON
carries `"shallow": true` plus a hint when that's the case, and `checkout`/
`diff` errors say so explicitly.

Run `crocs <command> --help` for flags.

## Output format

Every command emits JSON to stdout with a top-level envelope:

```json
{
  "_meta": {"crocs": "1", "command": "list"},
  "projects": ["..."]
}
```

`_meta.crocs` is the schema version — branch on it for forward-compatible
scripts. `_meta.command` is the invoking subcommand. Pass the global
`--compact` flag for unindented JSON (saves ~30-50% of the envelope tokens
on large `grep`/`symbols` output).

`read-files` is the sole carve-out and emits XML instead, because file
bodies are easier to read and debug as plain text than as JSON-escaped
strings:

```xml
<files crocs="1" command="read-files">
  <file path="bool.go" lines="1-25">
    <![CDATA[
       1 | package pflag
       ...
    ]]>
  </file>
</files>
```

## The agent + subagent loop

The point of crocs is the loop documented in `skills/crocs/SKILL.md`. The
main agent runs `summary` and `map` directly (cheap orientation), then
spawns subagents to grep the repo, judge candidate files, and return a
short list wrapped in `<files>…</files>` markers. The main agent reads
only the survivors with `read-files`.

The `<files>…</files>` wrapper is structural, not advisory: the main
agent discards anything outside the markers, so a subagent that leaks file
bodies or commentary wastes its own tokens, not the main agent's. This is
the same insight behind the lack of an MCP server — no shortcut around
the skill is the simplest way to enforce the contract.

`crocs install-skill` drops the skill into `~/.claude/skills/crocs/` so
Claude Code (or any agent honoring the skill convention) picks it up.

## `symbols`

`symbols` reads the symbol index built at fetch time (parse-once at
ingestion → instant queries forever). Supported languages: **Python,
TypeScript, TSX, Java, Go**.

```sh
crocs symbols cobra                              # everything in one project
crocs symbols cobra -p completions.go            # one file (parses fresh)
crocs symbols --name UserRepo --kind class       # across all tracked repos
crocs symbols --kind interface --lang go         # filter combinations
```

Records carry `kind` ("function", "method", "class", "interface",
"struct", "type", "enum", …), a `parent` field (enclosing class /
interface / struct / enum, or Go method receiver), and 1-based line
spans. Cross-project queries include the `project` field as the leading
column.

## Design

- **CGO-free.** Static binaries cross-compile to darwin/linux × {amd64,
  arm64} with no toolchain. `make cross` produces all three.
- **Pure-Go tree-sitter** via `github.com/odvcencio/gotreesitter`. Five
  grammars are subset-embedded into the binary at build time — see the
  `grammar_subset_*` tags in the `Makefile`.
- **SQLite** via `modernc.org/sqlite`. The registry lives at
  `$XDG_DATA_HOME/crocs/registry.db` (or `~/.local/share/crocs/`).
- **Git** via the system `git` CLI when present (blobless + shallow
  clones), with `go-git` as the in-process fallback.
- **`ripgrep`** for `grep` when present, pure-Go regex + parallel walk
  otherwise.
- **No server.** Agents reach crocs only through the CLI and the skill.

## Layout

```
cmd/crocs/      cobra wiring, one file per subcommand
internal/
  registry/     SQLite schema + projects/symbols CRUD
  vcs/          git CLI + go-git
  grepx/        ripgrep + pure-Go fallback
  symbols/      gotreesitter extraction pipeline
  files/        read-files XML bundler
  summary/      important-files + README excerpt
  treemap/      file walks shared by tree, map, detect
  detect/       extension → language histogram
  output/       JSON envelope writer
  project/      XDG paths + name normalization
  skill/        embedded SKILL.md
skills/crocs/   canonical SKILL.md (also embedded)
```

## License

MIT.
