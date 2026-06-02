package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"crocs/internal/output"
	"crocs/internal/registry"
	"crocs/internal/symbols"

	"github.com/spf13/cobra"
)

type symbolRecord struct {
	Project string `json:"project,omitempty"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line,omitempty"`
	Parent  string `json:"parent,omitempty"`
	Lang    string `json:"lang"`
}

type symbolsResponse struct {
	Name      string         `json:"name,omitempty"`
	Symbols   []symbolRecord `json:"symbols"`
	Count     int            `json:"count"`
	Truncated bool           `json:"truncated,omitempty"`
}

var (
	symPath       string
	symNamePat    string
	symKinds      []string
	symLang       string
	symProjects   []string
	symLimit      int
)

// defaultSymbolsLimit caps responses unless --limit overrides. Tuned to
// keep cross-project sweeps fast and the JSON payload skim-able.
const defaultSymbolsLimit = 500

var symbolsCmd = &cobra.Command{
	Use:   "symbols [name]",
	Short: "Show extracted function/class/method symbols",
	Long: `Read function/class/method/interface/type/enum definitions from the symbol
index built at fetch time. Three usage modes:

  crocs symbols <name>                    # everything in one project
  crocs symbols <name> -p <file>          # one file (always parses fresh)
  crocs symbols [--name X --kind class]   # cross-project (omit positional)

Symbol records carry kind ("function", "method", "class", ...), 1-based line
spans, language, and parent for methods (the receiver type for Go, the
enclosing class/interface elsewhere). In cross-project mode each record
includes "project" as a leading field.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runSymbols,
}

func init() {
	symbolsCmd.Flags().StringVarP(&symPath, "path", "p", "", "restrict to this one file (always parses fresh)")
	symbolsCmd.Flags().StringVar(&symNamePat, "name", "", "match symbol names (substring; or glob with * / ?; or LIKE with % / _)")
	symbolsCmd.Flags().StringSliceVar(&symKinds, "kind", nil, "filter by kind (comma-separated; e.g. function,method)")
	symbolsCmd.Flags().StringVar(&symLang, "lang", "", "filter by language (e.g. go,python,typescript,tsx,java)")
	symbolsCmd.Flags().StringSliceVar(&symProjects, "projects", nil, "cross-project: restrict to these projects (comma-separated)")
	symbolsCmd.Flags().IntVar(&symLimit, "limit", defaultSymbolsLimit, "max rows returned; 0 = no cap")
	rootCmd.AddCommand(symbolsCmd)
}

func runSymbols(cmd *cobra.Command, args []string) error {
	ctx := cmdCtx(cmd)
	db, err := openRegistry(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

	switch {
	case symPath != "" && len(args) != 1:
		return errors.New("-p/--path requires a positional project name")
	case symPath != "":
		return symbolsForFile(ctx, cmd, db, args[0], symPath)
	case len(args) == 1:
		return symbolsForProject(ctx, cmd, db, args[0])
	default:
		return symbolsCrossProject(ctx, cmd, db)
	}
}

// symbolsForFile is the on-the-fly path: parse the requested file in
// process and return its symbols. Bypasses the index entirely — one file
// takes a few ms to parse, fast enough not to bother reading the db. This
// also means `symbols -p X.go` works even on projects whose parsed_at is
// still NULL (e.g. fetched before this binary added eager parse).
func symbolsForFile(ctx context.Context, cmd *cobra.Command, db *registry.DB, name, rel string) error {
	p, err := requireProject(ctx, db, name)
	if err != nil {
		return err
	}
	if !symbols.IsSupported(rel) {
		return fmt.Errorf("language not supported for %s; supported: %s",
			rel, strings.Join(symbols.SupportedLanguages(), ", "))
	}
	abs := filepath.Join(p.Path, rel)
	fs, err := symbols.ExtractFile(abs)
	if err != nil {
		return err
	}
	recs := make([]symbolRecord, 0, len(fs.Symbols))
	for _, s := range fs.Symbols {
		if !matchesFilters(s.Name, s.Kind, s.Lang) {
			continue
		}
		recs = append(recs, symbolRecord{
			Path:    rel,
			Name:    s.Name,
			Kind:    s.Kind,
			Line:    s.Line,
			EndLine: s.EndLine,
			Parent:  s.Parent,
			Lang:    s.Lang,
		})
	}
	return output.Write(cmd.OutOrStdout(), "symbols", symbolsResponse{
		Name:    p.Name,
		Symbols: recs,
		Count:   len(recs),
	})
}

func symbolsForProject(ctx context.Context, cmd *cobra.Command, db *registry.DB, name string) error {
	p, err := requireProject(ctx, db, name)
	if err != nil {
		return err
	}
	q := buildQuery([]string{p.Name})
	rows, err := db.FindSymbols(ctx, q)
	if err != nil {
		return err
	}
	recs, trunc := rowsToRecords(rows, false, symLimit)
	return output.Write(cmd.OutOrStdout(), "symbols", symbolsResponse{
		Name:      p.Name,
		Symbols:   recs,
		Count:     len(recs),
		Truncated: trunc,
	})
}

func symbolsCrossProject(ctx context.Context, cmd *cobra.Command, db *registry.DB) error {
	// Determine the project universe: either user-specified --projects, or
	// every tracked project. We require at least one narrowing filter
	// (--name, --kind, --lang) to keep cross-project from returning
	// everything.
	if symNamePat == "" && len(symKinds) == 0 && symLang == "" {
		return errors.New("cross-project symbols requires at least --name, --kind, or --lang (otherwise the result would be the entire index)")
	}
	projects := symProjects
	if len(projects) == 0 {
		all, err := db.ListProjects(ctx)
		if err != nil {
			return err
		}
		for _, p := range all {
			projects = append(projects, p.Name)
		}
	}
	q := buildQuery(projects)
	rows, err := db.FindSymbols(ctx, q)
	if err != nil {
		return err
	}
	recs, trunc := rowsToRecords(rows, true, symLimit)
	return output.Write(cmd.OutOrStdout(), "symbols", symbolsResponse{
		Symbols:   recs,
		Count:     len(recs),
		Truncated: trunc,
	})
}

func buildQuery(projects []string) registry.SymbolQuery {
	return registry.SymbolQuery{
		Projects:    projects,
		NamePattern: registry.NormalizeGlob(symNamePat),
		Kinds:       flatten(symKinds),
		Lang:        symLang,
		Limit:       limitWithSlack(symLimit),
	}
}

// limitWithSlack returns a row cap one larger than the user's --limit so
// the caller can detect truncation. limit=0 means "no cap" — preserve that.
func limitWithSlack(limit int) int {
	if limit <= 0 {
		return 0
	}
	return limit + 1
}

func rowsToRecords(rows []registry.SymbolRow, withProject bool, limit int) ([]symbolRecord, bool) {
	truncated := false
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
		truncated = true
	}
	out := make([]symbolRecord, 0, len(rows))
	for _, r := range rows {
		rec := symbolRecord{
			Path:    r.Path,
			Name:    r.Name,
			Kind:    r.Kind,
			Line:    r.Line,
			EndLine: r.EndLine,
			Parent:  r.Parent,
			Lang:    r.Lang,
		}
		if withProject {
			rec.Project = r.Project
		}
		out = append(out, rec)
	}
	return out, truncated
}

// flatten expands user-supplied multi-flag values that may also contain
// commas (cobra StringSlice handles repeated -kind=X but bare comma input
// needs an extra split).
func flatten(vals []string) []string {
	if len(vals) == 0 {
		return nil
	}
	var out []string
	for _, v := range vals {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

// matchesFilters applies the in-memory equivalents of the SQL filters,
// used by the -p file path where we don't hit the db.
func matchesFilters(name, kind, lang string) bool {
	if symNamePat != "" {
		if !globMatch(name, symNamePat) {
			return false
		}
	}
	if len(symKinds) > 0 {
		ok := false
		for _, k := range flatten(symKinds) {
			if k == kind {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if symLang != "" && symLang != lang {
		return false
	}
	return true
}

// globMatch is the in-process counterpart of registry.NormalizeGlob — same
// behavior, but evaluated against a Go string rather than a SQL column.
func globMatch(s, pat string) bool {
	switch {
	case strings.ContainsAny(pat, "%_"):
		return likeMatch(s, pat)
	case strings.ContainsAny(pat, "*?"):
		return likeMatch(s, strings.NewReplacer("*", "%", "?", "_").Replace(pat))
	default:
		return strings.Contains(s, pat)
	}
}

// likeMatch implements SQL LIKE wildcards against a string. % matches any
// run including empty; _ matches exactly one rune.
func likeMatch(s, pat string) bool {
	// Standard 2-pointer LIKE matcher.
	si, pi := 0, 0
	starS, starP := -1, -1
	for si < len(s) {
		switch {
		case pi < len(pat) && (pat[pi] == '_' || pat[pi] == s[si]):
			si++
			pi++
		case pi < len(pat) && pat[pi] == '%':
			starP = pi
			starS = si
			pi++
		case starP != -1:
			pi = starP + 1
			starS++
			si = starS
		default:
			return false
		}
	}
	for pi < len(pat) && pat[pi] == '%' {
		pi++
	}
	return pi == len(pat)
}
