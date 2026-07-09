// Package treemap implements the file walks shared by `tree`, `map`, and
// `detect`. Paths returned are relative to the project root and always use
// forward slashes (so subagent prompts and stored paths look identical on
// macOS and Linux).
package treemap

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// Filters narrows a walk by path include/exclude lists. Empty Includes
// means "include everything"; non-empty means "include only matching
// paths". Excludes always wins over Includes. Matching is segment-aware:
// `src` matches `src/a.py` and `src` itself, but not `src2/b.py` — the
// same semantics crocs grep gives literal -i/-e filters.
type Filters struct {
	Includes []string
	Excludes []string
}

// match reports whether a relative path matches the filter set.
func (f Filters) match(rel string) bool {
	for _, e := range f.Excludes {
		if MatchPrefix(rel, e) {
			return false
		}
	}
	if len(f.Includes) == 0 {
		return true
	}
	for _, p := range f.Includes {
		if MatchPrefix(rel, p) {
			return true
		}
	}
	return false
}

// MatchPrefix reports whether pat names rel itself or an ancestor directory
// of it, segment-aware: `src` matches `src/a.py` and `src`, not `src2/b.py`.
// grepx uses the same predicate for its literal -i/-e filters so both
// backends agree on what a bare path means.
func MatchPrefix(rel, pat string) bool {
	if pat == "" {
		return false
	}
	pat = strings.TrimSuffix(pat, "/")
	return rel == pat || strings.HasPrefix(rel, pat+"/")
}

// defaultSkipDirs is hard-coded to keep walks fast on real repos. The .git
// directory is *always* skipped; users can re-include other dirs via -i.
var defaultSkipDirs = map[string]struct{}{
	".git": {},
}

// WalkFiles calls fn for every regular file under root with its
// slash-separated root-relative path. .git/ is unconditionally pruned.
// Unreadable subdirectories are skipped, not fatal — one bad permission bit
// must not blank out the whole listing. This is the walk shared by tree/map/
// detect (via Walk) and the grep fallback engine.
func WalkFiles(root string, fn func(rel, abs string, d fs.DirEntry)) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if _, skip := defaultSkipDirs[d.Name()]; skip && path != root {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil // .git gitlink file (submodule/worktree)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fn(filepath.ToSlash(rel), path, d)
		return nil
	})
}

// Walk returns every file path under root that survives the filter set,
// sorted. See WalkFiles for the walk semantics.
func Walk(root string, f Filters) ([]string, error) {
	var out []string
	err := WalkFiles(root, func(rel, _ string, _ fs.DirEntry) {
		if f.match(rel) {
			out = append(out, rel)
		}
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// DirCounts returns a per-directory file-count heatmap. Counts are inclusive
// of files in the directory itself only — they do NOT recurse. Producing a
// recursive total is the caller's responsibility (it's an O(n²) inflation
// most consumers don't want).
//
// The "." entry counts files at the repo root.
func DirCounts(root string, f Filters) ([]DirCount, error) {
	files, err := Walk(root, f)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, p := range files {
		dir := "."
		if i := strings.LastIndex(p, "/"); i >= 0 {
			dir = p[:i]
		}
		counts[dir]++
	}
	out := make([]DirCount, 0, len(counts))
	for d, n := range counts {
		out = append(out, DirCount{Path: d, FileCount: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FileCount != out[j].FileCount {
			return out[i].FileCount > out[j].FileCount
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// DirCount is one row of the directory heatmap.
type DirCount struct {
	Path      string `json:"path"`
	FileCount int    `json:"file_count"`
}
