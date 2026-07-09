// Package detect identifies languages by file extension. The mapping is
// intentionally small and curated; PLAN.md scopes Phase 0/1 to a handful of
// languages and we keep the table minimal so additions are deliberate.
package detect

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
)

// extToLang maps a lowercase file extension (with leading dot) to a
// canonical language label. Filenames-without-extension are matched against
// nameToLang.
var extToLang = map[string]string{
	".go":     "Go",
	".py":     "Python",
	".ts":     "TypeScript",
	".tsx":    "TSX",
	".js":     "JavaScript",
	".jsx":    "JSX",
	".java":   "Java",
	".rs":     "Rust",
	".rb":     "Ruby",
	".c":      "C",
	".h":      "C/C++ header",
	".cc":     "C++",
	".cpp":    "C++",
	".cxx":    "C++",
	".hpp":    "C++",
	".cs":     "C#",
	".kt":     "Kotlin",
	".swift":  "Swift",
	".scala":  "Scala",
	".clj":    "Clojure",
	".elm":    "Elm",
	".ex":     "Elixir",
	".exs":    "Elixir",
	".erl":    "Erlang",
	".hs":     "Haskell",
	".lua":    "Lua",
	".php":    "PHP",
	".pl":     "Perl",
	".pm":     "Perl",
	".sh":     "Shell",
	".bash":   "Shell",
	".zsh":    "Shell",
	".fish":   "Shell",
	".ps1":    "PowerShell",
	".sql":    "SQL",
	".html":   "HTML",
	".css":    "CSS",
	".scss":   "SCSS",
	".less":   "Less",
	".vue":    "Vue",
	".svelte": "Svelte",
	".md":     "Markdown",
	".rst":    "reStructuredText",
	".yaml":   "YAML",
	".yml":    "YAML",
	".toml":   "TOML",
	".json":   "JSON",
	".xml":    "XML",
	".proto":  "Protocol Buffers",
	".tf":     "Terraform",
	".nix":    "Nix",
	".dart":   "Dart",
	".zig":    "Zig",
	".v":      "V",
}

// nameToLang matches whole basenames (no extension) to a language label.
var nameToLang = map[string]string{
	"Dockerfile":          "Dockerfile",
	"Containerfile":       "Dockerfile",
	"Makefile":            "Make",
	"GNUmakefile":         "Make",
	"Justfile":            "Just",
	"Rakefile":            "Ruby",
	"Gemfile":             "Ruby",
	"build.gradle":        "Gradle",
	"settings.gradle":     "Gradle",
	"build.gradle.kts":    "Gradle (Kotlin DSL)",
	"settings.gradle.kts": "Gradle (Kotlin DSL)",
}

// Detect returns the canonical language label for a path, or "" if unknown.
func Detect(path string) string {
	name := filepath.Base(path)
	if lang, ok := nameToLang[name]; ok {
		return lang
	}
	ext := strings.ToLower(filepath.Ext(name))
	if ext == "" {
		return ""
	}
	return extToLang[ext]
}

// LangCount is one row of the language histogram.
type LangCount struct {
	Name      string  `json:"name"`
	FileCount int     `json:"file_count"`
	Share     float64 `json:"share"` // 0..1, rounded to 4 decimals
}

// Histogram counts files per language given a list of paths. Paths whose
// extension isn't in the table are bucketed under "Other"; if Other is the
// only result it's dropped from the output (no useful signal).
func Histogram(paths []string) []LangCount {
	totals := map[string]int{}
	for _, p := range paths {
		lang := Detect(p)
		if lang == "" {
			lang = "Other"
		}
		totals[lang]++
	}
	if len(totals) == 1 {
		if _, only := totals["Other"]; only {
			return nil
		}
	}
	out := make([]LangCount, 0, len(totals))
	total := 0
	for _, n := range totals {
		total += n
	}
	for name, n := range totals {
		share := 0.0
		if total > 0 {
			share = roundShare(float64(n) / float64(total))
		}
		out = append(out, LangCount{Name: name, FileCount: n, Share: share})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FileCount != out[j].FileCount {
			return out[i].FileCount > out[j].FileCount
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// roundShare rounds to four decimal places so shares are stable across
// runs. Rounded independently per language, so the column need not sum to
// exactly 1.0.
func roundShare(f float64) float64 {
	const scale = 10000.0
	return math.Round(f*scale) / scale
}
