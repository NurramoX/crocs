// Package skill ships the embedded crocs SKILL.md. The build copies
// skills/crocs/SKILL.md → internal/skill/SKILL.md before `go build` so
// the embed directive can pick it up — go:embed can't traverse `..`.
package skill

import _ "embed"

// Content is the SKILL.md bytes. go:embed fails the build if the staged
// file is missing, so Content can only be empty if an empty file was
// staged — install-skill refuses to install in that case.
//
//go:embed SKILL.md
var Content []byte
