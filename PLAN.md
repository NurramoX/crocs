# crocs (Go rewrite) — implementation plan

A greenfield rewrite of crocs in Go, with intentional breaking changes. The
Python crocs is treated as a working prototype; this is the real thing. The
design encodes the decisions from the architecture review: crocs is **fast
primitives + a skill**, the consumer is a **local Claude Code agent that spawns
disposable subagents**, and crocs stays **fast primitives only** — no ranking,
no graph, no relevance scoring in the tool itself.

---

## 0. Guiding principles (read before building)

1. **CGO-free, always.** Every dependency has a pure-Go option in 2026. This is
   the single most important build decision: it gives `CGO_ENABLED=0`
   cross-compilation, a one-line `go install`, and static binaries with no C
   toolchain. We accept a small, unnoticeable parsing-speed cost for it.
2. **crocs is dumb on purpose.** Primitives only: clone, grep, read, symbols,
   summary, tree. No ranking, no graph, no relevance scoring, no LLM calls. The
   intelligence lives in the agent + subagent loop, documented by the skill.
3. **The skill is a shipped artifact, not an afterthought.** The `crocs` skill
   (the SKILL.md drafted separately) ships in the repo and is the primary
   "feature" of the rewrite. The Go code exists to serve it.
4. **Eager at ingestion, cheap at query.** `fetch` does the expensive work once
   (clone + parse + persist symbols). Every later command is fast.
5. **Stable, scriptable output.** Subagents parse stdout. Every command supports
   a stable text format and a `--json` format. Output contracts are part of the
   API and breaking them is a breaking change.

---

## 1. Locked dependency choices (verified current as of 2026)

| Concern | Choice | Why | Reject |
|---|---|---|---|
| SQLite | `modernc.org/sqlite` (pure Go, `database/sql` driver) | CGO-free, full SQLite3 compat, actively maintained | `mattn/go-sqlite3` (CGO — breaks cross-compile, needs gcc) |
| tree-sitter | `github.com/odvcencio/gotreesitter` (pure-Go reimpl — no wasm, no purego dlopen). Track upstream, no commit pinning. Subset-embed only the 5 needed grammars (Python/TS/TSX/Java/Go) to keep binary <8MB | CGO-free, ships built-in `NewTagger` + auto-inferred tag queries matching the Aider / tree-sitter-language-pack convention, 1131-file Python parse in ~400ms | `malivvan/tree-sitter` (wasm/wazero, stale, no tagger), `smacker/go-tree-sitter` & `tree-sitter/go-tree-sitter` (both CGO) |
| git | `go-git/go-git/v5` (pure Go), with shell-`git` fallback for blobless/partial clone | CGO-free; covers clone/pull/log/diff/branches/tags | shelling `git` as the *only* path (adds a runtime dep) |
| CLI framework | `spf13/cobra` + `spf13/pflag` | matches the existing command/flag surface; subcommand ergonomics | `urfave/cli` (fine, but cobra is the ecosystem default) |
| grep engine | shell out to `ripgrep` if present, else pure-Go fallback (`regexp` + parallel walk) | rg is dramatically faster on huge repos (Pulsar); fallback keeps it self-contained | pure-Go only (too slow on 5k+ files) |

> **Phase 0 spike — explicit go/no-go criteria.** Phase 0 validates the
> *locked* gotreesitter library (no longer "evaluate options + validate"). Spike
> succeeds when:
> - all 5 target grammars (Python/TS/TSX/Java/Go) load under `CGO_ENABLED=0`
>   across darwin-arm64, linux-amd64, linux-arm64;
> - `NewTagger` returns at least function + class definition captures for one
>   ~200-line fixture per language;
> - a full parse of aider (~700 files) finishes in <5s on M-series hardware;
> - subset-embed produces a binary <8MB.
>
> Spike fails (and pure-Go is reconsidered) only if no grammar+tagger
> combination works end-to-end after ~2 focused days, OR the tagger
> consistently misses obvious definitions in 3+ fixtures per language.
> If one language works weirdly (e.g. TSX) but the others are clean, document
> that language as "limited support" — do not block the project on it.
>
> **No regex fallback.** crocs commits to tree-sitter for symbols. If a
> language's grammar genuinely fails, `symbols` skips that language entirely
> rather than falling back to a regex code path. See Breaking Change #3.

### The git decision, expanded

go-git is pure Go and handles the common operations, but it is **weaker on
partial/blobless clones** — and this session measured that you target large
repos (Superset ~9.7k files, Pulsar ~5.5k). The `--filter=blob:none` clone that
made those fast in testing is a `git` CLI feature. Decision: **detect a system
`git` at startup; if present, use it for `clone` (with blobless filtering) and
`pull`; use go-git for everything in-process (log, diff, branch/tag listing,
reading objects).** If no system git, fall back to go-git clone (full). This
keeps the tool usable with zero external deps while staying fast when `git`
exists. Document the dependency as optional-but-recommended.

---

## 2. Intended breaking changes vs. Python crocs

These are deliberate. Call them out in a MIGRATION note.

1. **JSON is the default output.** Every command emits JSON to stdout. No
   `--text` mode (humans skim with `jq` or accept the JSON). Every JSON
   response includes a top-level `"_meta": {"crocs": "1", "command": "..."}`
   envelope so consumers can branch on schema version. Breaking: scripts
   parsing the old text shape need to switch to `jq`/equivalent.
2. **`read-files` keeps the XML envelope.** Sole carve-out from JSON-default
   because it bundles file *bodies*; JSON-wrapping every newline/quote is ugly
   to read and debug. Add a schema/version attribute and make line-range +
   size limits first-class. Breaking: tag names/attributes may change.
3. **`symbols` is tree-sitter-backed only.** Pure tree-sitter via gotreesitter;
   no per-language regex fallback. Output gains `kind` precision
   (function/method/class/interface/type/enum) and accurate line spans.
   Breaking: symbol records have more/renamed fields; some previously-missed
   symbols now appear and some false positives disappear. If a grammar fails
   for a language, that language is excluded from `symbols`, not regex-faked.
4. **`fetch` does eager parsing.** It now parses + persists a symbol index at
   clone time (slower fetch, instant `symbols` later). Budget: ≤60s for
   `fetch` on the largest target repos (Superset, Pulsar). Breaking: `fetch`
   is no longer just a clone; it has a longer runtime and writes more to the
   registry.
5. **Registry schema v2.** New tables for the symbol index and parse metadata
   (see §4). Breaking: old Python registry is not read; `fetch` afresh. No
   importer — re-fetch is cheap.
6. **Single static binary, `XDG`-respecting paths.** Clones and the registry move
   to `$XDG_DATA_HOME/crocs` (or `~/.local/share/crocs`). Breaking: location
   change from whatever Python used.
7. **No MCP. No server.** **Intentional design**, not just deprioritized.
   Removing MCP makes the only agent-facing path to crocs the skill-via-Bash
   pattern — an agent can't shortcut around the skill by calling
   `mcp__crocs__grep` directly. This is the same insight as the SKILL's
   `<files>` wrapper: convert a prompt-level contract into a structural one
   by removing the bypass. Document this stance in MIGRATION.md as a feature,
   not a gap.
8. **`symbols` gains cross-project mode + filter flags.** New flags:
   `--name <pattern>`, `--kind <function|method|class|interface|type|enum>`,
   and **cross-project mode** triggered by omitting the project argument
   (searches all tracked repos). `--lang <go|python|...>` and
   `-p <proj1,proj2,...>` narrow the cross-project surface. Breaking: the
   `symbols` command signature now accepts no project as a valid form.

---

## 3. Project layout

```
crocs/
  cmd/crocs/            # main.go — cobra root, wires subcommands
  internal/
    registry/           # SQLite (modernc) — projects + symbol index
    vcs/                # clone/pull/log/diff/branches/tags (git CLI + go-git)
    grepx/              # ripgrep-shellout + pure-Go fallback
    symbols/            # tree-sitter extraction + per-language regex fallback
    files/              # read-files bundling (XML/JSON envelope, line ranges, size caps)
    summary/            # overview + important-files surfacing (#5 survivor)
    treemap/            # tree + directory heatmap (map)
    output/             # text + --json renderers, stable contracts
    project/            # name resolution, path layout, XDG dirs
  skills/
    crocs/
      SKILL.md          # the shipped skill (drafted separately)
  queries/              # vendored tree-sitter .scm tag queries per language
  go.mod
  README.md
  MIGRATION.md
```

---

## 4. Registry schema (SQLite v2)

```sql
CREATE TABLE projects (
  name        TEXT PRIMARY KEY,
  url         TEXT NOT NULL,
  path        TEXT NOT NULL,          -- clone location on disk
  default_ref TEXT,                   -- branch/tag checked out
  shallow     INTEGER NOT NULL,       -- 1 if blobless/shallow
  fetched_at  INTEGER NOT NULL,
  parsed_at   INTEGER                 -- null until symbol index built
);

CREATE TABLE symbols (
  project   TEXT NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
  path      TEXT NOT NULL,            -- file rel path
  name      TEXT NOT NULL,
  kind      TEXT NOT NULL,            -- function|method|class|interface|type|enum|...
  line      INTEGER NOT NULL,         -- 1-based start line
  end_line  INTEGER,
  parent    TEXT,                     -- enclosing symbol (e.g. class for a method), nullable
  lang      TEXT NOT NULL
);
CREATE INDEX idx_symbols_proj_path ON symbols(project, path);
CREATE INDEX idx_symbols_name_proj ON symbols(name, project);  -- leading on name: serves both in-project and cross-project name lookups
CREATE INDEX idx_symbols_kind_proj ON symbols(kind, project);  -- leading on kind: serves both in-project and cross-project kind filters
```

Notes:
- The symbol index is the **only** persisted derived artifact.
- `parent` gives you "method X of class Y" cheaply, which is what makes
  `symbols -p file.java` readable for a subagent without building a graph.
- Static snapshot ⇒ no mtime invalidation needed; reparse only on `update`.

---

## 5. Command surface (cobra) — preserve names, extend behavior

Keep every existing command name (no churn in the skill's command references).
**All commands emit JSON to stdout by default**, with a `_meta: {crocs: "1",
command: "..."}` envelope (see Breaking Change #1). Sole carve-out:
`read-files` emits the XML envelope (Breaking Change #2). Per-command notes:

- `fetch <url> [--name] [--full] [--ref]` — clone (blobless via git CLI if
  available) **then parse + persist symbols**. `--full` forces non-shallow.
  This is the eager-ingestion step. Budget ≤60s on the largest target repos.
- `list` — projects + `parsed_at` status.
- `summary <name>` — overview + language detect + **important-files**
  (README, dependency manifest, key config — the #5 survivor). See §6.
- `tree <name> [--include/-i] [--exclude/-e]` — one path per line (JSON array).
- `map <name>` — directory heatmap (file counts); how a subagent finds dense
  dirs to scope a search.
- `detect <name>` — languages + metadata.
- `grep <name> <pattern> [-i/-e/-C/--max-results/-M]` — preserve all existing
  flags exactly (the skill quotes them). ripgrep backend + pure-Go fallback.
- `read-files <name> <paths...> [--lines] [--max-size]` — **XML envelope**,
  versioned. The subagent reader. Does NOT emit JSON.
- `symbols [<name>] [-p/-i/-e] [--name PATTERN] [--kind KIND] [--lang LANG] [--projects PROJ1,PROJ2,...]`
  — tree-sitter-backed via gotreesitter; reads from the persisted index
  (instant) or parses on demand if `parsed_at` is null. **Cross-project mode**:
  omit `<name>` to search all tracked repos at once. `--name` matches symbol
  names (glob/regex). `--kind` filters by `function|method|class|interface|type|enum`
  (comma-separated for multi). `--lang` narrows by language. `--projects`
  narrows to a subset. When cross-project, output records carry the project
  as a leading field.
- `path <name>` / `info <name>` / `branches` / `tags` / `checkout` / `diff` /
  `log` / `update` / `update-all` / `remove` / `unshallow` — vcs + registry,
  via go-git (with git CLI for clone/pull).

`update`/`checkout`/`unshallow` must **reparse and refresh the symbol index**
(snapshot changed). Set `parsed_at` accordingly.

---

## 6. The #5 survivor: important-files in `summary`

Static list + filter, no new deps. In `internal/summary`, after the README
excerpt, detect and surface the project's "shape":

- Always: README*, CONTRIBUTING*, LICENSE*.
- Dependency manifests by ecosystem: `go.mod`, `package.json`, `pyproject.toml`,
  `requirements*.txt`, `Cargo.toml`, `pom.xml`, `build.gradle*`, `pinning.txt`,
  `mix.exs`, etc.
- Key config: `Dockerfile`, `docker-compose*`, `Makefile`, top-level `*.yaml`
  CI/config.

Emit them as a short list in `summary` output so the agent gets the project's
shape in one cheap call. This is the only brief feature beyond tree-sitter
symbols that ships in v1.

---

## 7. tree-sitter symbol extraction (the one real algorithm)

In `internal/symbols`, using `gotreesitter`:

1. Map file extension → language (Python, TS, TSX, Java, Go to start; expand
   later). Files with no supported grammar are **skipped and logged**, not
   regex-extracted. The plan commits to tree-sitter; if a language is
   genuinely broken, it's excluded from `symbols` until the grammar works.
2. Parse with `gotreesitter`. Use the built-in `NewTagger(lang, query, opts...)`
   which already implements the Aider / tree-sitter-language-pack
   `name.definition.*` capture convention. Either use the auto-inferred tags
   query (`grammars.LangEntry.TagsQuery` / `ResolveTagsQuery()`) or vendor an
   explicit `.scm` per language and pass it in — only the **definition**
   captures are needed.
3. For each definition capture, emit a symbol record: name, kind (from the
   capture's tag suffix), start/end line, enclosing parent (walk up the AST to
   the nearest class/interface), lang.
4. Bulk-insert into the `symbols` table within one transaction per file batch.

**Scope:** definitions only — name, kind, line span, enclosing parent. That's
exactly what makes `symbols -p <file>` a useful triage step for a subagent
deciding whether to read a file, and it's all the topic-query workload needs.

Performance target: full parse of Pulsar (~2.7k Java files) in a handful of
seconds at `fetch` time. Parsing is I/O-bound; parallelize the file walk with a
worker pool sized to GOMAXPROCS.

---

## 8. Build / release

- `CGO_ENABLED=0 go build ./cmd/crocs` must succeed on all targets.
- GoReleaser config cross-compiling darwin-{arm64,amd64}, linux-{amd64,arm64}.
  (CGO-free is what makes this a 90-second release instead of a toolchain
  matrix.)
- Vendored `.scm` queries embedded via `go:embed` so the binary is
  self-contained.
- `go install github.com/you/crocs/cmd/crocs@latest` should Just Work.

---

## 9. Phased sequencing

**Phase 0 — spikes (validate the locked stack).**
- Spike `gotreesitter`: load Python/TS/TSX/Java/Go grammars under `CGO_ENABLED=0`,
  run `NewTagger` against a 200-line fixture per language, confirm
  function+class captures and line spans. Measure aider full-parse wall time
  (<5s target). Verify subset-embed binary <8MB. See Section 1 for the explicit
  go/no-go criteria.
- Spike `modernc.org/sqlite`: open, migrate, bulk insert 25k rows, query
  (including the new `(name, project)` and `(kind, project)` indexes). Confirm
  CGO_ENABLED=0 build.
- Spike go-git clone vs git-CLI blobless clone on a large repo; measure. Lock
  the "git CLI if present" decision.

**Phase 1 — primitives parity (no symbols index yet).**
- Registry v2 (projects table), XDG paths, cobra skeleton.
- vcs: fetch (blobless), list, info, path, branches, tags, checkout, log, diff,
  update, remove, unshallow.
- grepx (ripgrep + pure-Go fallback), read-files (XML envelope, versioned),
  tree, map, detect.
- **JSON-default output + `_meta` envelope** on every command except `read-files`.
  Stable output contracts + golden tests.
- *Milestone:* the skill's grep+read+orient loop works end to end on a large
  tracked repo.

**Phase 2 — tree-sitter symbols + eager ingestion + cross-project queries.**
- symbols table, extraction pipeline via gotreesitter, parallel parse.
- Wire parsing into fetch/update/checkout/unshallow (full reparse on
  update/checkout; incremental is Phase 3).
- `symbols` reads the index; `symbols -p` reads/parses one file.
- `symbols --name <pat>`, `--kind <kind>`, and **cross-project mode** (omit
  project) ship in this phase. `--lang` and `--projects` filters too.
- *Milestone:* both work instantly: (a) `crocs symbols <repo> -p <file>`
  returns accurate, parented symbols for a subagent's triage, AND
  (b) `crocs symbols --name UserRepo --kind class` returns matches across all
  tracked repos with project as a leading field.

**Phase 3 — summary #5 + polish.**
- important-files surfacing in summary.
- README excerpt logic (skip badge-only lines — a Python-crocs strength to keep).
- MIGRATION.md, README, embed the skill.
- *Milestone:* `crocs summary` gives project shape in one call.

**Phase 4 — ship the skill + live validation.**
- Finalize `skills/crocs/SKILL.md`; install into local Claude Code.
- Run the real test: "which files handle auth?" on Superset via the spawn loop.
  Verify (a) subagents return a clean short list (no file bodies — the context
  contract), and (b) the list is actually the auth files, not the content-match
  dragnet. Harden the skill against whatever actually breaks.

---

## 10. Open questions / risks

### Still open (intentional defer)

1. **Single subagent vs. shard threshold** — the skill says shard by `-i` scope
   when a candidate set exceeds ~20 files; tune the threshold against real runs
   in Phase 4.

### Resolved

- **Pure-Go tree-sitter grammar coverage** — locked to `gotreesitter`, which
  ships all 5 target grammars (Python/TS/TSX/Java/Go) with built-in `NewTagger`.
  Phase 0 validates the specific library, not the category. See Section 1.
- **Subagent return-contract enforcement** — solved by the
  `<files>...</files>` wrap-and-extract pattern in `skills/crocs/SKILL.md`.
  The contract is structural (consumer-enforced) rather than prose-only.
  Validated end-to-end against Python crocs before scope-locking the rewrite:
  subagents naturally leak ~80 tokens of narration before the wrapper, but
  the main agent discards anything outside `<files>`.
- **Importer for the old Python registry** — skipped. Re-fetch is cheap. See
  Breaking Change #5.

### Standing risks (gotreesitter)

These don't need decisions today; they're signals to watch and respond to.

- **API churn (pre-1.0).** v0.19.x → v0.20 → v1.0 may break crocs's usage of
  `NewTagger` / `Tag` types. We track upstream (no commit pinning); accept
  occasional `go get -u` breakages and fix when they hit.
- **Grammar blob format drift.** Blobs are tied to tree-sitter's upstream
  parse-table ABI. If upstream changes that format, blobs need regeneration
  via the project's `ts2go` / `grammar_updater`. Watch for this.
- **Bus factor.** Single-author project, ~6 months old. If it goes dormant,
  contingency options (no commitment today): fork and self-maintain the
  runtime, switch to `malivvan/tree-sitter` and reimplement the tagger, or
  accept CGO via `smacker/go-tree-sitter`. React to signals, not
  hypotheticals.
