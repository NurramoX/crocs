// Package symbols extracts function/class/method definitions from source
// files via github.com/odvcencio/gotreesitter. Pure-Go (CGO_ENABLED=0). The
// 5 grammars committed to in PLAN.md §1 — Python, TypeScript, TSX, Java, Go
// — are the v1 surface. Files in other languages are skipped (no regex
// fallback per Breaking Change #3).
package symbols

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Symbol is one extracted definition from a single file.
type Symbol struct {
	Name    string
	Kind    string // function|method|class|interface|struct|type|enum|constant|variable|constructor
	Line    int    // 1-based start line
	EndLine int    // 1-based end line; 0 if unknown
	Parent  string // enclosing class/struct/interface/enum, or method receiver (Go); empty otherwise
	Lang    string // canonical language label ("go", "python", "typescript", "tsx", "java")
}

// FileSymbols is the result for one file.
type FileSymbols struct {
	Path    string // relative to the extract root
	Lang    string
	Symbols []Symbol
}

// supportedExts maps file extensions we know how to parse to a canonical
// language label. Anything not in this map is skipped during ExtractDir.
var supportedExts = map[string]string{
	".go":   "go",
	".py":   "python",
	".ts":   "typescript",
	".tsx":  "tsx",
	".java": "java",
}

// IsSupported reports whether the symbols package can extract definitions
// from a file at this path.
func IsSupported(path string) bool {
	_, ok := supportedExts[strings.ToLower(filepath.Ext(path))]
	return ok
}

// SupportedLanguages returns the canonical labels of languages we extract.
// Useful for the JSON --lang filter — narrow the universe at the call site
// rather than letting an unsupported value silently filter nothing.
func SupportedLanguages() []string {
	out := make([]string, 0, len(supportedExts))
	seen := map[string]struct{}{}
	for _, l := range supportedExts {
		if _, ok := seen[l]; !ok {
			out = append(out, l)
			seen[l] = struct{}{}
		}
	}
	sort.Strings(out)
	return out
}

// ExtractFile parses one file at abs and returns its symbols. Returns
// (FileSymbols{}, nil) for files in unsupported languages — that is not an
// error, it's the documented v1 behavior.
func ExtractFile(abs string) (FileSymbols, error) {
	lang, ok := supportedExts[strings.ToLower(filepath.Ext(abs))]
	if !ok {
		return FileSymbols{}, nil
	}
	entry := grammars.DetectLanguage(filepath.Base(abs))
	if entry == nil {
		return FileSymbols{}, nil
	}
	tg, err := newTagger(entry)
	if err != nil {
		return FileSymbols{}, fmt.Errorf("tagger %s: %w", lang, err)
	}
	src, err := os.ReadFile(abs)
	if err != nil {
		return FileSymbols{}, fmt.Errorf("read %s: %w", abs, err)
	}
	return FileSymbols{
		Path:    filepath.Base(abs),
		Lang:    lang,
		Symbols: tagsToSymbols(tg.Tag(src), src, lang),
	}, nil
}

// ExtractDir walks root and extracts symbols from every supported file.
// Returns per-file results; ordering is sorted by relative path so the
// output is deterministic. Parallelism is GOMAXPROCS-bound, with a
// per-language Tagger pool to amortize the (relatively expensive) NewTagger
// query compile.
//
// Errors on individual files (tagger construction, unreadable file) skip
// that file and continue — one bad file shouldn't halt a fetch. Returns a
// non-nil error only on catastrophic failure (e.g. walk error).
func ExtractDir(ctx context.Context, root string) ([]FileSymbols, int64, error) {
	type job struct {
		abs, rel string
		entry    *grammars.LangEntry
		lang     string
	}
	var jobs []job
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			n := d.Name()
			if n == ".git" || n == "node_modules" || n == "__pycache__" ||
				n == ".venv" || n == "venv" || n == "dist" || n == "build" ||
				n == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		lang, ok := supportedExts[ext]
		if !ok {
			return nil
		}
		entry := grammars.DetectLanguage(filepath.Base(path))
		if entry == nil {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		jobs = append(jobs, job{abs: path, rel: filepath.ToSlash(rel), entry: entry, lang: lang})
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if len(jobs) == 0 {
		return nil, 0, nil
	}

	// Pre-build one tagger per language. Tagger holds a Parser which is not
	// goroutine-safe; we use a per-language sync.Pool of taggers so each
	// goroutine can borrow one.
	taggerPools := sync.Map{}
	getTagger := func(entry *grammars.LangEntry) (*gts.Tagger, error) {
		lang := entry.Language()
		raw, _ := taggerPools.LoadOrStore(lang, &sync.Pool{
			New: func() any {
				tg, err := newTagger(entry)
				if err != nil {
					return err
				}
				return tg
			},
		})
		pool := raw.(*sync.Pool)
		v := pool.Get()
		if e, ok := v.(error); ok {
			return nil, e
		}
		return v.(*gts.Tagger), nil
	}
	putTagger := func(entry *grammars.LangEntry, tg *gts.Tagger) {
		if v, ok := taggerPools.Load(entry.Language()); ok {
			v.(*sync.Pool).Put(tg)
		}
	}

	results := make([]FileSymbols, len(jobs))
	var totalSyms atomic.Int64
	workers := runtime.GOMAXPROCS(0)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, j := range jobs {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			tg, err := getTagger(j.entry)
			if err != nil {
				return
			}
			src, err := os.ReadFile(j.abs)
			if err != nil {
				putTagger(j.entry, tg)
				return
			}
			syms := tagsToSymbols(tg.Tag(src), src, j.lang)
			putTagger(j.entry, tg)
			results[i] = FileSymbols{
				Path:    j.rel,
				Lang:    j.lang,
				Symbols: syms,
			}
			totalSyms.Add(int64(len(syms)))
		}()
	}
	wg.Wait()

	// Drop entries with no symbols and unparsed slots (extract errors).
	out := results[:0]
	for _, r := range results {
		if r.Path == "" {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, totalSyms.Load(), nil
}

// newTagger constructs a Tagger using ResolveTagsQuery — the canonical
// entry point. The README's `entry.TagsQuery` is empty for most languages
// (inferred from the grammar at runtime); see [[reference-gotreesitter-api]].
func newTagger(entry *grammars.LangEntry) (*gts.Tagger, error) {
	q := grammars.ResolveTagsQuery(*entry)
	if strings.TrimSpace(q) == "" {
		return nil, fmt.Errorf("no tags query available for %s", entry.Name)
	}
	return gts.NewTagger(entry.Language(), q)
}

// kindFromTag normalizes a gotreesitter tag kind like "definition.function"
// to a bare label like "function". Returns "" for non-definition tags
// (i.e. `reference.*`) so the caller can drop them.
func kindFromTag(tagKind string) string {
	const prefix = "definition."
	if !strings.HasPrefix(tagKind, prefix) {
		return ""
	}
	return tagKind[len(prefix):]
}

// containerKinds are the kinds we treat as "can be a parent" — used both
// for the range-containment parent-finder and as the kind filter when
// `--kind class` is supplied for a Python class that contains methods.
var containerKinds = map[string]struct{}{
	"class":     {},
	"interface": {},
	"struct":    {},
	"enum":      {},
}

// goMethodReceiverRe matches the Go method-declaration prefix and captures
// the receiver type name (with optional pointer-star and type parameters).
// Anchored at start.
//
//	func (d *Dog) Walk()     → "Dog"
//	func (a Animal) X()      → "Animal"
//	func (d *Dog[T]) Fetch() → "Dog"
var goMethodReceiverRe = regexp.MustCompile(`^\s*func\s*\(\s*\w+\s+\*?\s*([A-Za-z_][A-Za-z0-9_]*)\s*(?:\[[^\]]*\])?\s*\)`)

// tagsToSymbols converts a slice of gotreesitter Tags into our Symbol type,
// dropping non-definition tags and computing the Parent field where we can.
func tagsToSymbols(tags []gts.Tag, src []byte, lang string) []Symbol {
	// First pass: convert and drop refs.
	var defs []Symbol
	type ranged struct {
		startByte uint32
		endByte   uint32
	}
	ranges := make([]ranged, 0, len(tags))
	for _, t := range tags {
		k := kindFromTag(t.Kind)
		if k == "" {
			continue
		}
		defs = append(defs, Symbol{
			Name:    t.Name,
			Kind:    k,
			Line:    int(t.NameRange.StartPoint.Row) + 1,
			EndLine: int(t.Range.EndPoint.Row) + 1,
			Lang:    lang,
		})
		ranges = append(ranges, ranged{
			startByte: t.Range.StartByte,
			endByte:   t.Range.EndByte,
		})
	}

	// Second pass: parent computation. For each defs[i] find the smallest
	// other defs[j] whose Range contains defs[i].Range and whose kind is a
	// container. Linear over O(n^2) — n is typically <1000 per file so this
	// is fine; we can index by start-byte if it ever becomes a bottleneck.
	for i := range defs {
		// Special-case Go methods: take the receiver type name from the
		// method's source slice via regex. The Tag.Range for a method covers
		// `func (recv *T) Name(...)` so we can look at its starting bytes.
		if lang == "go" && defs[i].Kind == "method" {
			if recv := extractGoReceiver(src, ranges[i].startByte, ranges[i].endByte); recv != "" {
				defs[i].Parent = recv
				continue
			}
		}
		best := -1
		var bestSpan uint32 = ^uint32(0)
		for j := range defs {
			if i == j {
				continue
			}
			if _, ok := containerKinds[defs[j].Kind]; !ok {
				continue
			}
			if ranges[j].startByte > ranges[i].startByte {
				continue
			}
			if ranges[j].endByte < ranges[i].endByte {
				continue
			}
			span := ranges[j].endByte - ranges[j].startByte
			if span < bestSpan {
				bestSpan = span
				best = j
			}
		}
		if best >= 0 {
			defs[i].Parent = defs[best].Name
			// Some grammars (notably Python) emit "function" for both top-level
			// defs and class methods. Once we know a "function" lives inside a
			// container, promote it to "method" so the wire format is
			// consistent across languages.
			if defs[i].Kind == "function" {
				defs[i].Kind = "method"
			}
		}
	}
	return defs
}

// extractGoReceiver returns the receiver type name from a Go method's source
// slice, e.g. "Dog" from "func (d *Dog) Walk() error { ... }". Returns ""
// if the slice doesn't look like a method declaration.
func extractGoReceiver(src []byte, start, end uint32) string {
	if int(end) > len(src) {
		end = uint32(len(src))
	}
	// Look at the first ~120 bytes — receivers live close to `func`.
	probe := src[start:end]
	if len(probe) > 120 {
		probe = probe[:120]
	}
	m := goMethodReceiverRe.FindSubmatch(probe)
	if m == nil {
		return ""
	}
	return string(m[1])
}
