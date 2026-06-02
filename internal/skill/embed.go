// Package skill ships the embedded crocs SKILL.md. The build copies
// skills/crocs/SKILL.md → internal/skill/SKILL.md before `go build` so
// the embed directive can pick it up — go:embed can't traverse `..`.
package skill

import _ "embed"

// Content is the SKILL.md bytes. Empty in source builds before `make
// prepare` has copied the file in; the binary still builds — the
// install-skill subcommand will just write an empty file with a clear
// stderr warning.
//
//go:embed SKILL.md
var Content []byte
