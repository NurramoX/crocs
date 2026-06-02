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

// Filters narrows a walk by prefix include/exclude lists. Empty Includes
// means "include everything"; non-empty means "include only paths starting
// with any prefix in the list". Excludes always wins over Includes.
type Filters struct {
	Includes []string
	Excludes []string
}

// match reports whether a relative path matches the filter set.
func (f Filters) match(rel string) bool {
	for _, e := range f.Excludes {
		if e != "" && strings.HasPrefix(rel, e) {
			return false
		}
	}
	if len(f.Includes) == 0 {
		return true
	}
	for _, p := range f.Includes {
		if p != "" && strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

// defaultSkipDirs is hard-coded to keep walks fast on real repos. The .git
// directory is *always* skipped; users can re-include other dirs via -i.
var defaultSkipDirs = map[string]struct{}{
	".git": {},
}

// Walk returns every file path under root that survives the filter set.
// Paths are slash-separated and relative to root. .git/ is unconditionally
// pruned.
func Walk(root string, f Filters) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, skip := defaultSkipDirs[d.Name()]; skip && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !f.match(rel) {
			return nil
		}
		out = append(out, rel)
		return nil
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
