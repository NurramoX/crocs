---
name: crocs
description: "Locate which files handle a topic in a git repo via the crocs CLI, without reading them into the main context. Use when the user asks where a feature or subsystem lives, wants to orient in an unfamiliar repo, or wants to fetch an external repo — or when another task needs a locate-then-read pass over a codebase outside the working directory."
---

# crocs

Answer "where does X live, and what's its logic?" over a large repo **without
reading dozens of files into your own context**. You (the main agent) decide
*what* to look for; subagents do the *looking* — skimming the 80 candidate
files to find the 8 that matter — and their context is discarded the moment
they return their short ranked list. You read only the survivors.

**The doctrine: load the map at low resolution, zoom on demand.** The CLI is a
resolution ladder:

- **Low-res map** — `crocs summary` / `crocs map` / `crocs tree`. Load once per
  repo; it orients everything after.
- **Frontier queries** — `crocs grep` / `crocs symbols`. Tell you *where* to
  zoom without paying for file bodies.
- **Zoom** — `crocs read-files`. Full detail, paid per file. The only rung that
  loads code into a context.

Never zoom what a frontier query can answer, and never load the map at full
resolution (a repo-wide symbol dump, a blanket grep you read yourself).

## When to use this skill vs. just querying directly

**Delegate to a subagent (the default for topic/locate queries):**
- "Which files handle auth / brokers / caching / rate-limiting?"
- "Find everything related to <subsystem>, then explain how it works."
- Any query where the candidate set is large or unknown and you'd otherwise
  read many files to filter.

**Query crocs directly, no subagent (cheap, bounded lookups):**
- You already know the file(s) and just need to read them → `crocs read-files`.
- You need a quick orientation → `crocs summary` / `crocs tree` / `crocs map`.
- One narrow grep whose result you'll act on immediately → `crocs grep`.

If you can answer the question by reading ≤3 files you already identified, skip
the subagent and just read them.

## The crocs CLI surface

All commands take the tracked project NAME as the first argument.

```
crocs fetch <git-url>              # clone + track a repo (do this once)
crocs list                         # list tracked projects
crocs summary <name>               # overview + semantic metadata + shape (README, manifest, key config)
crocs tree <name>                  # one file path per line
crocs map <name>                   # directory heatmap (file counts, non-recursive) — find the dense dirs
crocs detect <name> [-i/-e path]   # language histogram, scopable to a subtree

crocs grep <name> <pattern> [opts] # regex search
    -i, --include TEXT             # only this path/dir or glob (repeatable; `src` ≠ `src2`)
    -e, --exclude TEXT             # exclude a path/dir or glob (repeatable)
    -C, --context INTEGER          # lines of context around each match
    --max-results INTEGER          # cap (default 200)
    -M, --multiline                # pattern may span lines (. crosses newlines)

crocs read-files <name> <paths...> [opts]   # bundle files as XML
    --lines TEXT                   # range, e.g. 200-300 (also lifts --max-size)
    --max-size INTEGER             # max file KB (default 100)

crocs symbols <name> -p <file>     # one file's function/class/method defs (parses fresh)
crocs symbols <name> [filters]     # whole-project defs from the index
crocs symbols [filters]            # cross-project index query (omit the name)
    --name TEXT                    # symbol name: substring, glob (* ?), or LIKE (% _)
    --kind TEXT                    # function,method,class,interface,struct,type,enum
    --lang TEXT                    # go,python,typescript,tsx,java (comma-separated)
    --limit INTEGER                # cap (default 500); --projects a,b scopes cross-project
```

Every JSON command also takes a global `--compact` (unindented JSON — cheaper
to read for large grep/symbols output).

## The workflow

### 1. Orient (you, once per repo, cheap)

```
crocs summary <name>
crocs map <name>          # which directories are dense — likely where subsystems live
```

This tells you the language mix and the rough layout so you can brief subagents
with sensible path scopes (e.g. "search under `src/` and `pkg/`, not `docs/`").

### 2. Expand the topic into search terms (you)

crocs has no semantic search — the synonym expansion is your job. Turn the
user's topic into a regex alternation of the vocabulary that would actually
appear in the relevant files. Start with **high-precision** terms
(only the topic uses them) before adding broad ones:

- "auth" → high-precision: `authenticat|authoriz|oauth|jwt|csrf|credential|login|logout|guest_token`. Broad (add if the first pass missed obvious files): `|session|permission|role|token`. The broad terms over-fire in dense codebases — "session" matches SQLAlchemy DB sessions, "role" matches chart roles, "token" matches lexer tokens — and burn the `--max-results` cap before the actual auth files are reached.
- "brokers" → `broker|topic|partition|subscription|producer|consumer|publish|dispatch`

Lexical *presence* is necessary but not sufficient — many files mention a token
once in passing. The subagent's job is to separate files that *handle* the topic
(dense, central) from files that merely *mention* it. That judgment is why you
delegate to an LLM subagent rather than just returning grep hits.

### 3. Decide the scope: single subagent or shard?

Before spawning, decide whether one subagent can cover the topic in one grep, or
whether you need to shard. Two triggers make sharding mandatory, not optional:

- **The scope crosses stacks.** If auth lives in both a Python backend
  (`superset/`) and a TypeScript frontend (`superset-frontend/src/`), a single
  grep with `-i backend/ -i frontend/src/` is biased — ripgrep walks one tree
  first and a `--max-results` cap will be eaten before it reaches the other.
  Shard: one subagent per stack.
- **The candidate set could exceed ~20 files.** Use `crocs map <name>` to
  predict density. Dense subsystems (e.g. anything touching auth/session/role
  in a large web app) almost always cross the threshold. Shard by top-level
  directory.

If both triggers are absent (narrow topic, single stack, small predicted set),
one subagent is fine. When in doubt, shard — two short subagent runs cost less
than one biased run you have to re-do. See "Sharding large candidate sets"
below for the mechanics.

**Fail fast before spawning.** A missing project or an empty scope should fail
here — in your context, cheaply — not inside two parallel subagents. Before
delegating, confirm the repo is tracked (step 1's `crocs summary` fails loudly
if not; `crocs fetch` it first) and that every `-i` scope you're about to hand
out actually appears in the `crocs map` output.

### 4. Delegate search-and-filter to subagent(s)

Spawn a subagent per scope. Brief it tightly, and brief
the *concept*, not guessed structure: describe what the code you want *does*
("code that maps triage roles to label strings") and add a question the
subagent can orient by ("where would a maintainer of X have to edit?"). Don't
pre-guess file paths or symbol names you haven't verified — a wrong guess
anchors the subagent on a dead end. The only paths in the brief should be the
`-i` scope globs you validated against `crocs map`. Template:

> You are searching the crocs-tracked repo `<name>` to find the files that
> **handle** <topic> (not files that merely mention it in passing).
>
> 1. Run: `crocs grep <name> "<expanded-regex>" -i <scope-glob> -C 2 --max-results 200`
> 2. **Check `truncated`.** If the response has `"truncated": true`, your sample
>    is biased toward whichever subtree ripgrep walked first — files outside
>    that subtree never appeared, so you cannot honestly filter it as-is. You
>    have two recovery options; pick one:
>    - **Re-grep with narrower scope.** Add more specific `-i` includes (e.g.
>      `-i superset/security/ -i superset/views/auth.py`) or more excludes
>      (`-e a_noisy_dir`) until `truncated` is false. Use *that* sample, not
>      the original.
>    - **Abort.** Emit exactly the empty wrapper `<files></files>` on a single
>      line and nothing else. The main agent treats an empty wrapper as "scope
>      too broad" and re-spawns with a narrower scope.
>
>    What you must NOT do: filter the truncated sample. It does not represent
>    the codebase.
> 3. From the hits, identify candidate files. For any file you're unsure about,
>    inspect its structure with `crocs symbols <name> -p <file>` and/or read the
>    relevant region with `crocs read-files <name> <file> --lines <range>`.
> 4. Decide which files are genuinely responsible for <topic>. A file qualifies
>    if the topic is a primary concern (dense matches, core definitions), not an
>    incidental reference (one stray token in an unrelated module).
>
>    **You are done when every file in your (non-truncated) grep hit set is
>    either ruled in or ruled out** — not when you've collected "some relevant
>    files." Returning after the first few obvious hits is a contract
>    violation: the main agent treats your list as the complete relevant set
>    for your scope.
>
>    **Gut-check via symbols.** For any candidate that's large (>500 lines) or
>    whose role you can't state in one sentence, run `crocs symbols <name> -p
>    <file>` and count how many symbols name topic concepts. If only a small
>    fraction (e.g. <10%) of definitions are about the topic, reject the file —
>    grep lit up incidental matches, not a topic-handling module. A 2000-line
>    models file with one topic-related class is mention-only, no matter how
>    many grep hits it produced.
> 5. **Output contract — strict.** Wrap your entire file list in
>    `<files>...</files>` markers. The main agent will discard anything outside
>    those markers, so any narration you write outside them is wasted work.
>    - **Wrapper:** start with `<files>` on its own line, end with `</files>` on
>      its own line.
>    - **Between the markers:** one file per line, exactly
>      `<path> — <tier> — <≤15-word role>`. Most-central first. `<tier>` is one
>      of: `core` (the topic's home — the main agent will read these), `related`
>      (participates but isn't the center), `speculative` (plausible from grep,
>      unverified by you).
>    - **Inside the markers:** nothing but list lines. No headers, no blank
>      lines, no quoted symbol/function names, no file contents, no commentary.
>    - **Outside the markers:** nothing. No preamble before `<files>`, no
>      trailing notes after `</files>`.
>
>    Good (do this):
>    ```
>    <files>
>    src/auth/login.py — core — password login flow and session creation
>    src/auth/oauth.py — core — OAuth2/OIDC provider integration
>    src/auth/middleware.py — related — request authentication middleware
>    src/config.py — speculative — appears to hold auth setting defaults
>    </files>
>    ```
>
>    Bad (do NOT do this):
>    ```
>    I looked at the auth files. Here's the list:
>
>    <files>
>    src/auth/login.py — handles login. Calls `do_login()` and creates sessions
>        - also session refresh
>    </files>
>
>    The login flow is central.
>    ```

Brief the subagent with the output contract and the good/bad examples
verbatim — they are load-bearing. Subagents reflexively narrate their
reasoning before answering; the wrapper is what turns the contract from a
polite request into a shape you can verify.

**Main-agent extraction step (you, after the subagent returns):** read only the
lines between `<files>` and `</files>`. Discard everything outside, including
any preamble or trailing commentary. Two special cases:

- **Empty wrapper** (`<files></files>` or `<files>` immediately followed by
  `</files>` with nothing between) — the subagent refused because `truncated`
  fired. Re-spawn with a narrower `-i` scope or shard by top-level directory.
  Do NOT brief the same subagent again with the same scope; you'll get the same
  refusal.
- **Missing or malformed markers** (no closing tag, content outside the
  wrapper, etc.) — the subagent broke the contract. Do not consume the leaky
  output; re-spawn with a sharper prompt that re-emphasizes the wrapper rule.

### 5. Read the survivors (you)

Now zoom just the handful the subagent flagged, in tier order:

```
crocs read-files <name> <core1> <core2> <core3>
```

Read `core` files first — they usually answer the question. Pull in `related`
files only where the core files reference them; touch `speculative` files only
if the picture is still incomplete after that.

## Version pinning & history (occasional)

The default fetch is a **shallow single-branch clone** — fast, but it means
`tags`/`branches`/`log` see only the fetched ref (their JSON says
`"shallow": true` with a hint when that's the case) and `checkout`/`diff`
can't reach other refs. To answer "as of release X" questions:

```
crocs unshallow <name>             # one-time: fetch full history, all branches + tags
crocs tags <name> -n 20            # discover release refs
crocs checkout <name> v2.3.0       # switch snapshot + rebuild the symbol index
crocs diff <name> --from v2.2.0 --to v2.3.0 --stat   # what changed between releases
```

`crocs checkout` (not raw git) is the right tool here because it also
reindexes symbols for the new snapshot.

## Sharding large candidate sets

Shard by path scope, **not** by splitting result sets. Use `crocs map` to find
the top-level packages, then spawn one subagent per package, each scoped with
`-i`:

```
subagent A:  crocs grep <name> "<regex>" -i src/server/
subagent B:  crocs grep <name> "<regex>" -i src/client/
subagent C:  crocs grep <name> "<regex>" -i pkg/
```

Each subagent filters its own slice and returns a short list; you merge and
dedupe the lists (they're short — merging is cheap and stays in your context).
The triggers that make sharding mandatory live in workflow step 3.

## Quick reference

| Goal | Command / action |
|------|------------------|
| Track a new repo | `crocs fetch <git-url>` |
| Orient | `crocs summary <name>`, `crocs map <name>` |
| Find candidates | `crocs grep <name> "<regex>" -i <scope> -C 2` |
| Inspect one file's shape | `crocs symbols <name> -p <file>` |
| Read a region | `crocs read-files <name> <file> --lines 200-300` |
| Read final survivors | `crocs read-files <name> <f1> <f2> <f3>` |
| Topic search over large repo | spawn subagent(s), shard by `-i` path scope |
