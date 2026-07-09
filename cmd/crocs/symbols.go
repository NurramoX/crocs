package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"crocs/internal/output"
	"crocs/internal/project"
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
	symPath     string
	symNamePat  string
	symKinds    []string
	symLang     string
	symProjects []string
	symLimit    int
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
	symbolsCmd.Flags().StringVar(&symLang, "lang", "", "filter by language, comma-separated (of: "+strings.Join(symbols.SupportedLanguages(), ", ")+")")
	symbolsCmd.Flags().StringSliceVar(&symProjects, "projects", nil, "cross-project: restrict to these projects (comma-separated)")
	symbolsCmd.Flags().IntVar(&symLimit, "limit", defaultSymbolsLimit, "max rows returned; 0 = no cap")
	rootCmd.AddCommand(symbolsCmd)
}

func runSymbols(cmd *cobra.Command, args []string) error {
	ctx := cmdCtx(cmd)
	if _, err := parseLangs(); err != nil {
		return err
	}
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
	abs, err := project.ResolveInRoot(p.Path, rel)
	if err != nil {
		return err
	}
	fs, err := symbols.ExtractFile(abs)
	if err != nil {
		return err
	}
	kinds := flatten(symKinds)
	langs, _ := parseLangs() // already validated in runSymbols
	recs := make([]symbolRecord, 0, len(fs.Symbols))
	truncated := false
	for _, s := range fs.Symbols {
		if !matchesFilters(s.Name, s.Kind, s.Lang, kinds, langs) {
			continue
		}
		if symLimit > 0 && len(recs) >= symLimit {
			truncated = true
			break
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
		Name:      p.Name,
		Symbols:   recs,
		Count:     len(recs),
		Truncated: truncated,
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
	langs, _ := parseLangs() // already validated in runSymbols
	return registry.SymbolQuery{
		Projects:    projects,
		NamePattern: registry.NormalizeGlob(symNamePat),
		Kinds:       flatten(symKinds),
		Langs:       langs,
		Limit:       limitWithSlack(symLimit),
	}
}

// parseLangs splits and validates the --lang flag. Comma-separated values
// are accepted ("go,python"); unknown labels are an error rather than a
// filter that silently matches nothing.
func parseLangs() ([]string, error) {
	if symLang == "" {
		return nil, nil
	}
	supported := map[string]struct{}{}
	for _, l := range symbols.SupportedLanguages() {
		supported[l] = struct{}{}
	}
	langs := flatten([]string{symLang})
	for _, l := range langs {
		if _, ok := supported[l]; !ok {
			return nil, fmt.Errorf("unsupported --lang %q; supported: %s",
				l, strings.Join(symbols.SupportedLanguages(), ", "))
		}
	}
	return langs, nil
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
func matchesFilters(name, kind, lang string, kinds, langs []string) bool {
	if symNamePat != "" {
		if !globMatch(name, symNamePat) {
			return false
		}
	}
	if len(kinds) > 0 {
		ok := false
		for _, k := range kinds {
			if k == kind {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(langs) > 0 {
		ok := false
		for _, l := range langs {
			if l == lang {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// globMatch is the in-process counterpart of registry.NormalizeGlob — same
// behavior as SQLite LIKE, including its ASCII case-insensitivity, so
// `--name repo` matches UserRepository in -p mode exactly as it does in the
// indexed modes.
func globMatch(s, pat string) bool {
	switch {
	case strings.ContainsAny(pat, "%_"):
		return likeMatch(s, pat)
	case strings.ContainsAny(pat, "*?"):
		return likeMatch(s, strings.NewReplacer("*", "%", "?", "_").Replace(pat))
	default:
		return strings.Contains(asciiLower(s), asciiLower(pat))
	}
}

// likeMatch implements SQL LIKE wildcards against a string: % matches any
// run including empty, _ matches exactly one rune, and letter comparison is
// ASCII case-insensitive (SQLite's default LIKE semantics).
func likeMatch(s, pat string) bool {
	rs, rp := []rune(s), []rune(pat)
	// Standard 2-pointer LIKE matcher.
	si, pi := 0, 0
	starS, starP := -1, -1
	for si < len(rs) {
		switch {
		case pi < len(rp) && (rp[pi] == '_' || likeRuneEq(rp[pi], rs[si])):
			si++
			pi++
		case pi < len(rp) && rp[pi] == '%':
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
	for pi < len(rp) && rp[pi] == '%' {
		pi++
	}
	return pi == len(rp)
}

func likeRuneEq(a, b rune) bool {
	if 'A' <= a && a <= 'Z' {
		a += 'a' - 'A'
	}
	if 'A' <= b && b <= 'Z' {
		b += 'a' - 'A'
	}
	return a == b
}

func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}
