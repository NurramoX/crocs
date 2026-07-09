package main

import (
	"context"
	"fmt"

	"crocs/internal/registry"
	"crocs/internal/symbols"
	"crocs/internal/vcs"
)

// reparseProject is the canonical "the snapshot just changed, refresh the
// symbol index" helper — a shared helper, not a CLI command. Called from
// fetch (initial population) and every op that changes (or may never have
// indexed) the working tree: checkout, update, update-all, unshallow.
// PLAN.md §5: "update/checkout/unshallow must reparse and refresh the
// symbol index (snapshot changed)."
//
// Returns the number of symbols persisted so callers can include it in
// their JSON response.
func reparseProject(ctx context.Context, db *registry.DB, p registry.Project) (int, error) {
	files, _, err := symbols.ExtractDir(ctx, p.Path)
	if err != nil {
		return 0, fmt.Errorf("extract symbols: %w", err)
	}
	rows := make([]registry.SymbolInput, 0, 1024)
	for _, fs := range files {
		for _, s := range fs.Symbols {
			rows = append(rows, registry.SymbolInput{
				Project: p.Name,
				Path:    fs.Path,
				Name:    s.Name,
				Kind:    s.Kind,
				Line:    s.Line,
				EndLine: s.EndLine,
				Parent:  s.Parent,
				Lang:    fs.Lang,
			})
		}
	}
	if err := db.ReplaceSymbols(ctx, p.Name, rows); err != nil {
		return 0, fmt.Errorf("persist symbols: %w", err)
	}
	return len(rows), nil
}

// syncAfterPull decides, after a pull, whether the symbol index must be
// rebuilt: yes when HEAD moved away from before, when either hash read
// failed (reindexing needlessly is safe, serving a stale index is not), or
// when the project was never indexed. Returns whether HEAD moved, the
// symbol count, and any reindex error. Shared by update and update-all.
func syncAfterPull(ctx context.Context, db *registry.DB, p registry.Project, before string) (changed bool, nSym int, reindexErr error) {
	after, _ := vcs.HeadHash(p.Path)
	changed = before == "" || after == "" || before != after
	if changed || p.ParsedAt == nil {
		nSym, reindexErr = reparseProject(ctx, db, p)
		return changed, nSym, reindexErr
	}
	// HEAD didn't move; the persisted index is still valid.
	if n, err := db.CountSymbols(ctx, p.Name); err == nil {
		nSym = n
	}
	return changed, nSym, nil
}
