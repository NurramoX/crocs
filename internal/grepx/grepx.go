// Package grepx is the grep primitive: ripgrep when available (dramatically
// faster on large repos — PLAN.md §1), pure-Go regex fallback otherwise.
// Output is a stable struct so the cobra layer just serializes it.
package grepx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Options controls a single grep call.
type Options struct {
	Pattern    string   // raw pattern (regex)
	Includes   []string // path-prefix include filters
	Excludes   []string // path-prefix exclude filters
	Context    int      // lines of context before & after each match
	MaxResults int      // hard cap; 0 means use DefaultMaxResults
	Multiline  bool     // pattern may span lines
}

// DefaultMaxResults is the cap used when Options.MaxResults is zero.
// Mirrors Python crocs default so the skill's flag examples carry over.
const DefaultMaxResults = 200

// Match is one matched line plus its context.
type Match struct {
	Path          string   `json:"path"`
	Line          int      `json:"line"`
	Column        int      `json:"column,omitempty"`
	Text          string   `json:"text"`
	ContextBefore []string `json:"context_before,omitempty"`
	ContextAfter  []string `json:"context_after,omitempty"`
}

// Result is what Run produces.
type Result struct {
	Matches   []Match `json:"matches"`
	Truncated bool    `json:"truncated"`
	Engine    string  `json:"engine"` // "ripgrep" | "go"
}

// Run executes a grep against root using the best available backend.
func Run(ctx context.Context, root string, opts Options) (Result, error) {
	if opts.MaxResults <= 0 {
		opts.MaxResults = DefaultMaxResults
	}
	if _, err := exec.LookPath("rg"); err == nil {
		return runRipgrep(ctx, root, opts)
	}
	return runGo(ctx, root, opts)
}

// --- ripgrep backend -----------------------------------------------------

func runRipgrep(ctx context.Context, root string, opts Options) (Result, error) {
	// Local cancellable child context so we can SIGTERM ripgrep when we've
	// collected enough matches — otherwise it keeps writing into a pipe we've
	// stopped reading and cmd.Wait() blocks forever.
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := []string{"--json", "--no-heading"}
	if opts.Context > 0 {
		args = append(args, fmt.Sprintf("--context=%d", opts.Context))
	}
	if opts.Multiline {
		args = append(args, "--multiline")
	}
	for _, inc := range opts.Includes {
		args = append(args, "--glob", makeRGGlob(inc))
	}
	for _, exc := range opts.Excludes {
		args = append(args, "--glob", "!"+makeRGGlob(exc))
	}
	args = append(args, "--", opts.Pattern, ".")

	cmd := exec.CommandContext(cctx, "rg", args...)
	cmd.Dir = root
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("rg stdout pipe: %w", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("start rg: %w", err)
	}

	res := Result{Engine: "ripgrep"}
	matches, truncated := parseRipgrepJSON(stdout, opts)
	res.Matches = matches
	res.Truncated = truncated

	if truncated {
		// Stop ripgrep before we wait — and drain anything still queued so the
		// child doesn't block on the broken pipe.
		cancel()
		_, _ = io.Copy(io.Discard, stdout)
	}

	werr := cmd.Wait()
	// rg exit codes: 0=matches, 1=no match, 2=error. A SIGTERM (-15) from our
	// cancel() is also expected when we hit the cap.
	if exitErr := (*exec.ExitError)(nil); errors.As(werr, &exitErr) {
		code := exitErr.ExitCode()
		if code == 1 || (truncated && code < 0) {
			werr = nil
		} else {
			return Result{}, fmt.Errorf("rg failed (%v): %s", werr, strings.TrimSpace(stderr.String()))
		}
	} else if werr != nil && !truncated {
		return Result{}, fmt.Errorf("rg wait: %w: %s", werr, strings.TrimSpace(stderr.String()))
	}
	return res, nil
}

// makeRGGlob converts a user-supplied path prefix into a ripgrep glob. If
// the input already looks like a glob (contains * or ?), pass it through.
func makeRGGlob(s string) string {
	if strings.ContainsAny(s, "*?") {
		return s
	}
	s = strings.TrimSuffix(s, "/")
	return s + "/**"
}

// parseRipgrepJSON streams rg's --json output and assembles Match records.
// We collect context entries between matches and attach them to the next
// match (before) or the most recent match (after).
func parseRipgrepJSON(r interface {
	Read(p []byte) (n int, err error)
}, opts Options) ([]Match, bool) {
	type submatch struct {
		Start int `json:"start"`
	}
	type lineData struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int        `json:"line_number"`
		Submatches []submatch `json:"submatches"`
	}
	type event struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var (
		matches  []Match
		pending  []string // context_before lines queued for the next match
		lastIdx  = -1     // index of the most recent match in `matches`
	)

	for scanner.Scan() {
		var ev event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "begin":
			pending = pending[:0]
			lastIdx = -1
		case "context":
			var ld lineData
			if err := json.Unmarshal(ev.Data, &ld); err != nil {
				continue
			}
			text := strings.TrimRight(ld.Lines.Text, "\n")
			// If we have a most-recent match still building context_after, give
			// this line to it. Otherwise queue it as context_before for the
			// next match.
			if lastIdx >= 0 && len(matches[lastIdx].ContextAfter) < opts.Context {
				matches[lastIdx].ContextAfter = append(matches[lastIdx].ContextAfter, text)
			} else {
				pending = append(pending, text)
				if opts.Context > 0 && len(pending) > opts.Context {
					pending = pending[len(pending)-opts.Context:]
				}
			}
		case "match":
			if len(matches) >= opts.MaxResults {
				return matches, true
			}
			var ld lineData
			if err := json.Unmarshal(ev.Data, &ld); err != nil {
				continue
			}
			col := 0
			if len(ld.Submatches) > 0 {
				col = ld.Submatches[0].Start + 1
			}
			m := Match{
				Path:   strings.TrimPrefix(ld.Path.Text, "./"),
				Line:   ld.LineNumber,
				Column: col,
				Text:   strings.TrimRight(ld.Lines.Text, "\n"),
			}
			if len(pending) > 0 {
				m.ContextBefore = append([]string(nil), pending...)
				pending = pending[:0]
			}
			matches = append(matches, m)
			lastIdx = len(matches) - 1
		case "end":
			pending = pending[:0]
			lastIdx = -1
		}
	}
	return matches, false
}

// --- pure-Go backend -----------------------------------------------------

func runGo(ctx context.Context, root string, opts Options) (Result, error) {
	pattern := opts.Pattern
	flags := "(?m)" // multi-line semantics for ^/$
	if opts.Multiline {
		// Cross-line patterns need (?s) so . matches newline.
		flags = "(?ms)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		return Result{}, fmt.Errorf("compile pattern: %w", err)
	}

	type job struct {
		rel  string
		abs  string
		size int64
	}
	var jobs []job
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !pathMatches(rel, opts.Includes, opts.Excludes) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		// Skip absurdly large files (>10MB) — they're typically build artifacts
		// or vendored blobs and would dominate the scan.
		if info.Size() > 10<<20 {
			return nil
		}
		jobs = append(jobs, job{rel: rel, abs: path, size: info.Size()})
		return nil
	})
	if walkErr != nil {
		return Result{}, walkErr
	}

	type fileResult struct {
		matches []Match
	}
	results := make([]fileResult, len(jobs))

	workers := runtime.GOMAXPROCS(0)
	if workers > 8 {
		workers = 8
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, j := range jobs {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		default:
		}
		i, j := i, j
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = fileResult{matches: searchFile(j.abs, j.rel, re, opts)}
		}()
	}
	wg.Wait()

	out := Result{Engine: "go"}
	for _, fr := range results {
		for _, m := range fr.matches {
			if len(out.Matches) >= opts.MaxResults {
				out.Truncated = true
				return out, nil
			}
			out.Matches = append(out.Matches, m)
		}
	}
	sort.SliceStable(out.Matches, func(i, j int) bool {
		if out.Matches[i].Path != out.Matches[j].Path {
			return out.Matches[i].Path < out.Matches[j].Path
		}
		return out.Matches[i].Line < out.Matches[j].Line
	})
	return out, nil
}

func searchFile(abs, rel string, re *regexp.Regexp, opts Options) []Match {
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil
	}
	if !isProbablyText(data) {
		return nil
	}
	lines := strings.Split(string(data), "\n")

	var out []Match
	if opts.Multiline {
		// Find all matches against the full source; map byte offsets back to
		// line numbers. Slow on big files but bounded by the 10MB cap above.
		locs := re.FindAllIndex(data, -1)
		for _, loc := range locs {
			line, col := offsetToLineCol(data, loc[0])
			out = append(out, buildMatch(rel, line, col, lines, opts.Context))
		}
		return out
	}
	for i, l := range lines {
		if locs := re.FindStringIndex(l); locs != nil {
			out = append(out, buildMatch(rel, i+1, locs[0]+1, lines, opts.Context))
		}
	}
	return out
}

func buildMatch(rel string, line, col int, lines []string, ctxN int) Match {
	m := Match{Path: rel, Line: line, Column: col, Text: lines[line-1]}
	if ctxN > 0 {
		start := line - 1 - ctxN
		if start < 0 {
			start = 0
		}
		end := line + ctxN
		if end > len(lines) {
			end = len(lines)
		}
		if start < line-1 {
			m.ContextBefore = append([]string(nil), lines[start:line-1]...)
		}
		if line < end {
			m.ContextAfter = append([]string(nil), lines[line:end]...)
		}
	}
	return m
}

func offsetToLineCol(data []byte, off int) (line, col int) {
	line = 1
	col = 1
	for i := 0; i < off && i < len(data); i++ {
		if data[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return
}

func pathMatches(rel string, includes, excludes []string) bool {
	for _, e := range excludes {
		if e != "" && strings.HasPrefix(rel, e) {
			return false
		}
	}
	if len(includes) == 0 {
		return true
	}
	for _, inc := range includes {
		if inc != "" && strings.HasPrefix(rel, inc) {
			return true
		}
	}
	return false
}

// isProbablyText returns true if the first 8KiB of the buffer look like
// text. We treat a NUL byte in the first chunk as the canonical signal of
// "binary".
func isProbablyText(b []byte) bool {
	if len(b) > 8192 {
		b = b[:8192]
	}
	for _, c := range b {
		if c == 0 {
			return false
		}
	}
	return true
}
