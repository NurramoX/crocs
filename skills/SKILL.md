---
name: crocs
description: "Explore an unfamiliar git repository to answer topic/location questions like 'which files handle auth?' or 'which files relate to brokers?' without bloating the main agent's context. Uses the crocs CLI (fetch, grep, read-files, symbols, summary, tree) and delegates the read-heavy search-and-filter work to disposable subagents that return only a short list of relevant files. Trigger when the user asks where something lives in a codebase, which files are responsible for a feature/subsystem, or asks to find-then-understand code in a repo crocs tracks. Also trigger on 'explore the repo', 'find the files that do X', 'where is X handled', or any locate-then-read workflow over a large codebase."
---

# crocs

Answer "where does X live, and what's its logic?" over a large repo **without
reading dozens of files into your own context**. The pattern: you (the main
agent) decide *what* to look for and delegate the *looking* to subagents whose
context is disposable. They grep, read, and judge; they return a short ranked
file list with one-line rationales. You read only the handful that matter.

This exists because the expensive part of code exploration is the *reading* —
skimming 80 candidate files to find the 8 that matter. Done in your context,
that's 80 files of tokens you carry for the rest of the session. Done in a
subagent, it's discarded the moment the subagent returns its list.

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
the subagent and just read them. The subagent earns its cost only when it
saves *you* from reading a large candidate set.

## The crocs CLI surface

All commands take the tracked project NAME as the first argument.

```
crocs fetch <git-url>              # clone + track a repo (do this once)
crocs list                         # list tracked projects
crocs summary <name>               # concise overview + semantic metadata + project shape
crocs tree <name>                  # one file path per line
crocs map <name>                   # directory heatmap (file counts) — find the dense dirs
crocs detect <name>                # languages + metadata

crocs grep <name> <pattern> [opts] # regex search
    -i, --include TEXT             # only paths matching prefix/glob (repeatable)
    -e, --exclude TEXT             # exclude paths matching prefix/glob (repeatable)
    -C, --context INTEGER          # lines of context around each match
    --max-results INTEGER          # cap (default 200)
    -M, --multiline                # pattern may span lines

crocs read-files <name> <paths...> [opts]   # bundle files as XML
    --lines TEXT                   # range, e.g. 200-300
    --max-size INTEGER             # max file KB (default 100)

crocs symbols <name> [opts]        # function/class/method defs
    -p, --path TEXT                # one specific file
    -i/-e include/exclude globs
```

`crocs grep` is the candidate-finder. `crocs read-files` is the reader (use
`--lines` to read a signature region instead of a whole large file). `crocs
symbols -p <file>` gives a file's structure so a subagent can decide whether a
file is worth fully reading. `crocs summary` already surfaces the project's
shape (README, dependency manifest, key config) — read it first on an
unfamiliar repo.

## The workflow

### 1. Orient (you, once per repo, cheap)

```
crocs summary <name>
crocs map <name>          # which directories are dense — likely where subsystems live
```

This tells you the language mix and the rough layout so you can brief subagents
with sensible path scopes (e.g. "search under `src/` and `pkg/`, not `docs/`").

### 2. Expand the topic into search terms (you)

You're an LLM — do the synonym expansion yourself; crocs has no semantic search.
Turn the user's topic into a regex alternation of the vocabulary that would
actually appear in the relevant files:

- "auth" → `authenticat|authoriz|login|logout|oauth|jwt|token|credential|session|permission|role`
- "brokers" → `broker|topic|partition|subscription|producer|consumer|publish|dispatch`

Lexical *presence* is necessary but not sufficient — many files mention a token
once in passing. The subagent's job is to separate files that *handle* the topic
(dense, central) from files that merely *mention* it. That judgment is why you
delegate to an LLM subagent rather than just returning grep hits.

### 3. Delegate search-and-filter to subagent(s)

Spawn a subagent (Claude Code Task tool) per topic. Brief it tightly. Template:

> You are searching the crocs-tracked repo `<name>` to find the files that
> **handle** <topic> (not files that merely mention it in passing).
>
> 1. Run: `crocs grep <name> "<expanded-regex>" -i <scope-glob> -C 2 --max-results 200`
> 2. From the hits, identify candidate files. For any file you're unsure about,
>    inspect its structure with `crocs symbols <name> -p <file>` and/or read the
>    relevant region with `crocs read-files <name> <file> --lines <range>`.
> 3. Decide which files are genuinely responsible for <topic>. A file qualifies
>    if the topic is a primary concern (dense matches, core definitions), not an
>    incidental reference (one stray token in an unrelated module).
> 4. **Output contract — strict.** Wrap your entire file list in
>    `<files>...</files>` markers. The main agent will discard anything outside
>    those markers, so any narration you write outside them is wasted work.
>    - **Wrapper:** start with `<files>` on its own line, end with `</files>` on
>      its own line.
>    - **Between the markers:** one file per line, exactly
>      `<path> — <≤15-word role>`. Most-central first.
>    - **Inside the markers:** nothing but list lines. No headers, no blank
>      lines, no quoted symbol/function names, no file contents, no commentary.
>    - **Outside the markers:** nothing. No preamble before `<files>`, no
>      trailing notes after `</files>`.
>
>    Good (do this):
>    ```
>    <files>
>    src/auth/login.py — password login flow and session creation
>    src/auth/oauth.py — OAuth2/OIDC provider integration
>    src/auth/middleware.py — request authentication middleware
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

The `<files>...</files>` wrapper is what makes the output contract enforceable
rather than a polite request. Subagents have a strong reflex to summarize their
reasoning before answering and prompt-level "don't narrate" instructions only
partially suppress it. The wrapper converts the contract into a verifiable
shape: the main agent extracts only what's between the markers and ignores
everything else. When you brief the subagent, include the good/bad examples
verbatim — they are load-bearing.

**Main-agent extraction step (you, after the subagent returns):** read only the
lines between `<files>` and `</files>`. Discard everything outside, including
any preamble or trailing commentary. If the markers are absent or malformed
(no closing tag, content outside, etc.), the subagent broke the contract — do
not consume the leaky output; re-spawn with a sharper prompt instead.

### 4. Read the survivors (you)

Now read just the handful the subagent flagged:

```
crocs read-files <name> <file1> <file2> <file3>
```

You spent ~3 files of context, not 80, and you have an accurate relevant set.

## Sharding large candidate sets

If a topic spans a big repo (e.g. grep would hit 80+ files), **shard by path
scope, not by splitting result sets**. Use `crocs map` to find the top-level
packages, then spawn one subagent per package, each scoped with `-i`:

```
subagent A:  crocs grep <name> "<regex>" -i src/server/
subagent B:  crocs grep <name> "<regex>" -i src/client/
subagent C:  crocs grep <name> "<regex>" -i pkg/
```

Each subagent filters its own slice and returns a short list; you merge and
dedupe the lists (they're short — merging is cheap and stays in your context).
Sharding by scope keeps each subagent's reading bounded and avoids one subagent
drowning in 80 files. Prefer one subagent when the candidate set is small;
shard only when a single subagent would have to read more than ~20 files.

## Keep the context clean

- The relevant set comes back from subagents as a short list. Resist the urge to
  pre-load a repo-wide symbol dump into your own context; let the subagents do
  the reading and return only what matters.

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
