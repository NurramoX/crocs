// Package summary surfaces a project's "shape" in one cheap call:
// README excerpt (badge-filtered), important manifest/config files, and
// the language histogram.
package summary

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxReadmeBytes caps the size of the README we'll read off disk. Excerpt
// extraction operates on the prefix only; 32 KiB is enough for a normal
// project overview without slurping a 200 KB monorepo manifesto.
const MaxReadmeBytes = 32 * 1024

// MaxExcerptLines caps the prose returned in the JSON. Tuned to "first
// substantive paragraph or two" — agents skim this to orient, they don't
// read the whole README.
const MaxExcerptLines = 40

// Readme is the README excerpt result. Empty path means no README was
// found at the project root.
type Readme struct {
	Path    string
	Excerpt string
}

// alwaysGlobs are filename prefixes we always include if present at the
// project root. Case-insensitive.
var alwaysGlobs = []string{
	"README",
	"CONTRIBUTING",
	"LICENSE",
	"COPYING",
	"NOTICE",
	"CHANGELOG",
}

// manifestNames are dependency-manifest filenames by ecosystem. Files at
// the project root only — manifests in subdirectories belong to subpackages
// and would be noise here.
var manifestNames = map[string]struct{}{
	"go.mod":           {},
	"go.work":          {},
	"package.json":     {},
	"pnpm-lock.yaml":   {},
	"yarn.lock":        {},
	"pyproject.toml":   {},
	"setup.py":         {},
	"setup.cfg":        {},
	"Pipfile":          {},
	"poetry.lock":      {},
	"Cargo.toml":       {},
	"pom.xml":          {},
	"build.gradle":     {},
	"build.gradle.kts": {},
	"settings.gradle":  {},
	"mix.exs":          {},
	"Gemfile":          {},
	"Gemfile.lock":     {},
	"composer.json":    {},
	"pubspec.yaml":     {},
	"deno.json":        {},
	"deno.jsonc":       {},
	"shard.yml":        {},
	"stack.yaml":       {},
	"cabal.project":    {},
	"Project.toml":     {},
	"DESCRIPTION":      {}, // R
	"flake.nix":        {},
	"default.nix":      {},
}

// requirementsRe matches requirements*.txt files (pip).
var requirementsRe = regexp.MustCompile(`(?i)^requirements.*\.txt$`)

// configNames are key non-dependency config files at the project root.
var configNames = map[string]struct{}{
	"Dockerfile":          {},
	"Containerfile":       {},
	"docker-compose.yml":  {},
	"docker-compose.yaml": {},
	"Makefile":            {},
	"GNUmakefile":         {},
	"Justfile":            {},
	"justfile":            {},
	"Taskfile.yml":        {},
	"Taskfile.yaml":       {},
}

// configExts contains extensions for top-level CI/config yaml files
// (project-conventional locations like .github/workflows/*.yml are
// NOT in scope — we surface only what's at the project root).
var configExts = map[string]struct{}{
	".yaml": {},
	".yml":  {},
	".toml": {},
}

// configBaseAllowList only emits top-level *.yaml/*.yml that look like CI
// or release config; arbitrary yamls would be noise.
var configBaseAllowList = []string{
	".github", ".gitlab",
	"goreleaser",
	"ci",
	"renovate",
	"netlify",
	"vercel",
	"trunk",
}

// ImportantFiles returns the set of root-level files we want to surface,
// sorted by category and then alphabetically. The list is intentionally
// short — this is meant to be a glance, not a full directory listing.
func ImportantFiles(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	var keep []string
	for _, e := range entries {
		if e.IsDir() {
			// Top-level dirs are never important-files; ImportantFiles is
			// scoped to root. We surface them indirectly via map/tree.
			continue
		}
		name := e.Name()
		if isAlways(name) || isManifest(name) || isConfig(name) {
			keep = append(keep, name)
		}
	}

	sort.SliceStable(keep, func(i, j int) bool {
		ci, cj := categoryRank(keep[i]), categoryRank(keep[j])
		if ci != cj {
			return ci < cj
		}
		return strings.ToLower(keep[i]) < strings.ToLower(keep[j])
	})
	return keep, nil
}

func isAlways(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range alwaysGlobs {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func isManifest(name string) bool {
	if _, ok := manifestNames[name]; ok {
		return true
	}
	if requirementsRe.MatchString(name) {
		return true
	}
	return false
}

func isConfig(name string) bool {
	if _, ok := configNames[name]; ok {
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	if _, ok := configExts[ext]; ok {
		base := strings.ToLower(strings.TrimSuffix(name, ext))
		for _, prefix := range configBaseAllowList {
			if strings.Contains(base, prefix) {
				return true
			}
		}
	}
	return false
}

// categoryRank groups important files so the output reads naturally:
//
//	0 — README + CONTRIBUTING + LICENSE + friends
//	1 — dependency manifests
//	2 — config / build / CI
//
// alphabetical within group.
func categoryRank(name string) int {
	switch {
	case isAlways(name):
		return 0
	case isManifest(name):
		return 1
	default:
		return 2
	}
}

// FindReadme returns the README filename (e.g. "README.md", "README.rst",
// "README") found at root, preferring extensioned versions in conventional
// order. Empty result + nil error means no README is present.
func FindReadme(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", err
	}
	preferredOrder := []string{".md", ".rst", ".markdown", ".adoc", ".org", ".txt", ""}
	pick := func(ext string) string {
		want := "readme" + strings.ToLower(ext)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := strings.ToLower(e.Name())
			if n == want {
				return e.Name()
			}
		}
		return ""
	}
	for _, ext := range preferredOrder {
		if n := pick(ext); n != "" {
			return n, nil
		}
	}
	return "", nil
}

// ReadReadme reads the project README and returns its excerpt + the
// filename it came from. Returns Readme{} (zero value) + nil if the
// project has no README.
func ReadReadme(root string) (Readme, error) {
	name, err := FindReadme(root)
	if err != nil || name == "" {
		return Readme{}, err
	}
	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return Readme{}, err
	}
	defer f.Close()

	buf := make([]byte, MaxReadmeBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Readme{}, err
	}
	data := buf[:n]
	if n == MaxReadmeBytes {
		// The cap may have split a multi-byte UTF-8 rune — trim the partial
		// tail rather than emitting a mangled final character.
		for i := 0; i < 3 && len(data) > 0; i++ {
			r, size := utf8.DecodeLastRune(data)
			if r != utf8.RuneError || size != 1 {
				break
			}
			data = data[:len(data)-1]
		}
	}
	return Readme{
		Path:    name,
		Excerpt: Excerpt(string(data)),
	}, nil
}

// Excerpt drops badge lines, HTML banners, and leading boilerplate so the
// caller sees the project's substantive intro. Capped at MaxExcerptLines.
//
// What we strip:
//   - shields.io / badgen.net / images via Markdown image syntax or HTML <img>
//   - HTML <p align="…"> / <div align="…"> wrappers (always — they exist to
//     center decorative content in READMEs, never substantive prose)
//   - bare HTML structural tags with no text content (<a href>…</a>, <hr>, <br>)
//   - empty lines at the very start
//
// What we keep: fenced code blocks pass through unmodified, since their `<`
// characters are syntax, not markup. Mixed-prose lines stay.
func Excerpt(content string) string {
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")

	out := make([]string, 0, MaxExcerptLines)
	leadingBlank := true
	htmlSkipDepth := 0
	inFence := false

	for _, raw := range lines {
		if len(out) >= MaxExcerptLines {
			break
		}
		trimmed := strings.TrimSpace(raw)

		// Fenced code blocks pass through verbatim.
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			out = append(out, raw)
			leadingBlank = false
			continue
		}
		if inFence {
			out = append(out, raw)
			leadingBlank = false
			continue
		}

		// Already inside an aligned HTML banner block? Skip until close,
		// tracking nested banner opens so an inner <p align> doesn't let its
		// </p> end the outer block early and leak the rest into the excerpt.
		if htmlSkipDepth > 0 {
			opens := alignBannerOpen(trimmed)
			closes := hasHTMLClose(trimmed, "p") || hasHTMLClose(trimmed, "div")
			switch {
			case opens && !closes:
				htmlSkipDepth++
			case closes && !opens:
				htmlSkipDepth--
			}
			continue
		}
		// Opening a <p align> / <div align> block → skip until close.
		if alignBannerOpen(trimmed) {
			if !(hasHTMLClose(trimmed, "p") || hasHTMLClose(trimmed, "div")) {
				htmlSkipDepth++
			}
			continue
		}
		if isBadgeLine(trimmed) {
			continue
		}
		if isContentlessHTML(trimmed) {
			continue
		}

		if trimmed == "" {
			if leadingBlank {
				continue
			}
			out = append(out, "")
			continue
		}
		leadingBlank = false
		out = append(out, raw)
	}

	// Collapse runs of blank lines so the excerpt isn't full of holes after
	// we stripped multiple banners.
	out = collapseBlanks(out)

	// Trim trailing blanks for tidy output.
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n")
}

// alignBannerOpen reports whether the line opens a <p align=…> or
// <div align=…> banner. These are exclusively used in READMEs to center
// decorative content (logos, badges, sponsor blocks) — never substantive
// prose, so we always drop the whole block.
func alignBannerOpen(line string) bool {
	low := strings.ToLower(line)
	if !hasTagPrefix(low, "p") && !hasTagPrefix(low, "div") {
		return false
	}
	return strings.Contains(low, "align=") || strings.Contains(low, `style="text-align`)
}

// hasTagPrefix reports whether the (lowercased) line starts with the given
// HTML tag at a tag boundary — "<p " matches, "<pre" does not.
func hasTagPrefix(low, tag string) bool {
	if !strings.HasPrefix(low, "<"+tag) {
		return false
	}
	rest := low[len(tag)+1:]
	if rest == "" {
		return true
	}
	switch rest[0] {
	case ' ', '\t', '>', '/':
		return true
	}
	return false
}

// isContentlessHTML returns true if the line is structural HTML with no
// substantive text — `<a href=…>`, `</a>`, `<hr>`, `<br>`, `<sup>foo</sup>`
// containing just label text. Heuristic: strip all tags, see if anything
// non-trivial remains.
func isContentlessHTML(line string) bool {
	if !strings.HasPrefix(line, "<") && !strings.HasPrefix(line, "</") {
		return false
	}
	stripped := stripAllTags(line)
	stripped = strings.TrimSpace(stripped)
	return stripped == "" ||
		// short labels that are obviously decoration ("Supported by:", "Sponsor")
		(len(stripped) < 30 && !strings.ContainsAny(stripped, ".:;"))
}

// stripAllTags removes everything between `<…>` (single line only — no
// state tracked across lines). Used only for the contentless-line check;
// the original line is what gets emitted to the excerpt.
func stripAllTags(s string) string {
	var b strings.Builder
	inTag := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				b.WriteByte(s[i])
			}
		}
	}
	return b.String()
}

func collapseBlanks(lines []string) []string {
	out := lines[:0]
	prevBlank := false
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, l)
			continue
		}
		prevBlank = false
		out = append(out, l)
	}
	return out
}

// isBadgeLine returns true for the canonical Markdown patterns README
// authors use for status badges:
//
//	[![Build](https://shields.io/...)](https://ci...)
//	![GoDoc](https://godoc.org/...)
//	<img src="https://shields.io/..."/>
//
// We err on the side of keeping content — only lines that are ONLY badges/
// images get dropped. Mixed-prose lines stay.
func isBadgeLine(line string) bool {
	if line == "" {
		return false
	}
	// Linked-image badge: [![alt](img-url)](link-url) possibly repeated.
	if linkedBadgeRe.MatchString(line) {
		return true
	}
	// Bare image lines: ![alt](url) possibly repeated.
	if bareImageOnlyRe.MatchString(line) {
		return true
	}
	// Single-tag HTML image line: <img src="..." />
	if htmlImageOnlyRe.MatchString(line) {
		return true
	}
	// HTML link wrapping an image: <a href="..."><img …/></a>
	if htmlLinkedImageRe.MatchString(line) {
		return true
	}
	return false
}

var (
	// `[![alt](url)](link)` repeated, possibly separated by whitespace.
	linkedBadgeRe = regexp.MustCompile(`^(\s*\[!\[[^\]]*\]\([^)]*\)\]\([^)]*\)\s*)+$`)
	// `![alt](url)` repeated, possibly separated by whitespace.
	bareImageOnlyRe = regexp.MustCompile(`^(\s*!\[[^\]]*\]\([^)]*\)\s*)+$`)
	// `<img src="…"…>` or `<img …/>` possibly repeated.
	htmlImageOnlyRe = regexp.MustCompile(`^(\s*<img\b[^>]*/?>(\s|</img>)*)+$`)
	// `<a …><img …/></a>` possibly repeated.
	htmlLinkedImageRe = regexp.MustCompile(`^(\s*<a\b[^>]*>\s*<img\b[^>]*/?>\s*(</a>\s*)+)+$`)
)

func hasHTMLClose(line, tag string) bool {
	return strings.Contains(strings.ToLower(line), "</"+tag+">")
}
