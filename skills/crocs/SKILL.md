---
name: crocs
description: "Explore an external git repo through the crocs CLI. Use when the user asks where something lives in a repo outside the working directory, wants to orient in or fetch one, or asks about a specific release or what changed between versions."
---

# crocs

Answer "where does X live, and what's its logic?" over a large repo while
your own context holds only the files that **handle** X. crocs is a
resolution ladder — load the map once, zoom on demand:

- **Map** — `crocs summary` / `crocs map` / `crocs tree`. Low resolution,
  loaded once per repo; it orients everything after.
- **Probe** — `crocs grep` / `crocs symbols`. Says *where* to zoom without
  paying for file bodies.
- **Zoom** — `crocs read-files`. Full detail, paid per file; the only rung
  that loads code into a context.

Answer every question from the lowest rung that can answer it.

Every command takes a checkout handle as its first argument: the tracked
repo `NAME`, or `NAME@REF` for a pinned version (see "Versions").
`crocs <command> --help` lists the flags; the facts it leaves out:

- `-i src` scopes to the directory `src`, so `src2/` stays out. `-i` and `-e`
  take paths or globs and repeat.
- `crocs grep` stops at `--max-results` (default 200) and reports
  `"truncated": true`. ripgrep walks one subtree at a time, so a truncated
  sample covers only the subtrees walked first. Treat only a sample with
  `truncated` false as representative.
- `crocs read-files --lines 200-300` reads a region and lifts the 100 KB
  `--max-size` limit.
- `crocs symbols <name> -p <file>` parses one file fresh;
  `crocs symbols <name> [--name --kind --lang]` queries the project index;
  omitting `<name>` queries across projects (`--projects a,b` scopes it).
- The global `--compact` emits unindented JSON; use it on large grep and
  symbols output.

## Probe directly or delegate?

**Probe directly** when the lookup is bounded: you already know the files, you
need orientation, or one narrow grep gives a result you act on immediately.
Three or fewer known files: zoom them yourself.

**Delegate** when the candidate set is large or unknown ("which files handle
auth?", "find everything related to <subsystem>"). Subagents skim the 80
candidates to find the 8 that handle the topic, and their context is
discarded once they return a short ranked list. The steps below are the
delegate path.

## The delegate workflow

### 1. Map

```
crocs fetch <git-url>     # once, if `crocs list` lacks the repo
crocs summary <name>
crocs map <name>          # directory heatmap: the dense dirs are where subsystems live
```

Done when you can name the stacks (language plus top-level directory) and the
dense directories a subagent could be scoped to.

### 2. Expand the topic into a regex

crocs search is lexical, so synonym expansion is your job. Build an
alternation from **high-precision** terms — vocabulary only the topic uses:

- "auth" → `authenticat|authoriz|oauth|jwt|csrf|credential|login|logout`
- "brokers" → `broker|partition|subscription|producer|consumer|publish|dispatch`

Hold broad terms (`session|role|token`) in reserve: in a dense codebase they
match unrelated code (DB sessions, lexer tokens) and fill `--max-results`
before the topic's files appear. Add them only when a first pass returns
nothing from a dense directory where step 1 says the topic should live.

Done when every term would be surprising to find in a file unrelated to the
topic.

### 3. Scope: one subagent per shard

A shard is a set of `-i` path scopes one subagent can cover with an untruncated
grep. One shard suffices for a narrow topic in a single stack. Shard when:

- **The topic crosses stacks** (a Python backend and a TypeScript frontend):
  one shard per stack.
- **The candidate set could exceed ~20 files**, judged from `crocs map`
  density: one shard per dense top-level directory.

```
subagent A:  -i src/server/
subagent B:  -i src/client/
subagent C:  -i pkg/
```

When in doubt, shard: two short runs cost less than one truncated run redone.

Done when every `-i` scope you are about to hand out appears verbatim in the
`crocs map` output.

### 4. Delegate

Spawn one subagent per shard, in parallel. Fill the placeholders in
[`BRIEF.md`](BRIEF.md) (same directory as this file) and send the rest
verbatim. Describe the topic by what the code *does* ("code that decides
whether a request is allowed") plus a question to orient by ("where would a
maintainer of X have to edit?"). The only paths in a brief are the `-i` scopes
from step 3.

From each reply, keep only the lines between `<files>` and `</files>`:

- **List lines** — merge and dedupe across shards.
- **Empty wrapper** — the shard truncated. Split it into narrower `-i` scopes
  and re-spawn.
- **Missing or malformed wrapper** — discard the whole reply and re-spawn the
  shard.

Done when every shard has returned a well-formed, non-empty list, or an empty
one after its scope could be narrowed no further.

### 5. Zoom the survivors

```
crocs read-files <name> <core1> <core2> <core3>
```

Zoom in tier order: `core` first, `related` only where a core file references
it, `speculative` only if a gap remains after that.

Done when you can name the topic's entry point and trace its main path through
the files you read.

## Versions

A version is its own checkout beside the default one. Pin it once, then use
the handle everywhere a name goes, subagent briefs included:

```
crocs checkout <name> v2.3.0       # ~2s, fetches the ref on demand → handle <name>@v2.3.0
crocs grep <name>@v2.3.0 "<regex>" -i <scope>
crocs symbols --name Foo --projects <name>,<name>@v2.3.0   # compare across versions
crocs diff <name> --from v2.2.0 --to v2.3.0 --stat         # what changed between releases
crocs remove <name>@v2.3.0         # drop the checkout when done (keeps the repo)
```

`<ref>` is a tag, a branch, or a commit hash. Each checkout has its own symbol
index, so `symbols` answers are exact per version.

Only *history* needs more: the default fetch is a shallow single-branch clone,
so `tags` / `branches` / `log` list just the refs fetched so far and say
`"shallow": true`. `crocs unshallow <name>` fetches everything once;
`checkout` and `diff` work on the shallow clone.
