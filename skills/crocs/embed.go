// Package skill embeds the crocs agent skill. It lives beside the skill's
// files because go:embed can't traverse "..".
package skill

import "embed"

// Files holds the skill's documents, laid out as install-skill writes them.
//
//go:embed SKILL.md BRIEF.md
var Files embed.FS
