You are searching the crocs-tracked repo `<name>` for the files that
**handle** <topic>: files where it is a primary concern (dense matches, core
definitions). A file that **mentions** it — a stray token in an unrelated
module — is ruled out. <orienting-question>

1. Run: `crocs grep <name> "<regex>" <-i scopes> -C 2 --compact`
2. Check `truncated`. When it is `true` the sample covers only the subtrees
   ripgrep walked first. Re-grep with narrower `-i` scopes or added `-e`
   excludes until `truncated` is false, and work from that sample. If it stays
   true, reply with exactly `<files></files>` and stop.
3. Rule every file in the hit set in or out. For a file you are unsure of,
   inspect its shape with `crocs symbols <name> -p <file>` or read the region
   with `crocs read-files <name> <file> --lines <range>`. For a file over 500
   lines, or one whose role you cannot state in a sentence, count its symbols:
   when under ~10% name topic concepts, the file mentions the topic — a
   2000-line models file with one topic class is ruled out however many hits
   it produced.

   You are done when every file in the hit set is ruled in or ruled out. The
   main agent treats your list as the complete set for your scope.
4. Reply with the list and only the list; the main agent reads only what is
   between the markers. `<files>` on its own line, one line per file, `</files>`
   on its own line. Each line is exactly `<path> — <tier> — <role in ≤15 words>`,
   most central first. Tiers: `core` (the topic's home), `related`
   (participates, off-center), `speculative` (plausible from grep, unverified).

```
<files>
src/auth/login.py — core — password login flow and session creation
src/auth/oauth.py — core — OAuth2/OIDC provider integration
src/auth/middleware.py — related — request authentication middleware
src/config.py — speculative — appears to hold auth setting defaults
</files>
```
