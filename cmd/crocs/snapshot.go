package main

import (
	"context"
	"fmt"
	"time"

	"crocs/internal/registry"
	"crocs/internal/symbols"
	"crocs/internal/vcs"
)

// reparseProject is the canonical "the working tree just changed, refresh
// the symbol index" helper. Called from fetch (initial population),
// checkout (a new worktree), and update / update-all (a moved one).
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

// advanceResult is what advanceCheckout reports back: the ref the checkout
// now tracks, whether its working tree moved, and the symbol index state.
type advanceResult struct {
	Ref          string
	Kind         string
	Before       string // commit before the advance
	Commit       string
	Changed      bool
	SymbolCount  int
	ReindexError error
}

// advanceCheckout brings one checkout up to date. A checkout on a local
// branch (the default checkout after a plain fetch) is pulled; a detached
// one re-resolves its ref against origin — no network for a commit-pinned
// checkout, since a hash never moves — and moves to the resolved commit if
// HEAD isn't there (a moved branch or tag, or a hand-tampered worktree).
// The symbol index is rebuilt only when HEAD moved (or was never built).
// Shared by update and update-all.
func advanceCheckout(ctx context.Context, db *registry.DB, p registry.Project) (advanceResult, error) {
	before, _ := vcs.HeadHash(p.Path)
	ref, kind := p.Ref, p.Kind
	if branch, ok := vcs.OnBranch(p.Path); ok {
		if err := vcs.Pull(ctx, p.Path, branch); err != nil {
			return advanceResult{}, err
		}
		ref, kind = branch, string(vcs.RefBranch)
	} else {
		target := p.Commit
		if kind != string(vcs.RefCommit) {
			res, err := vcs.ResolveRef(ctx, p.RepoPath, ref, vcs.ResolveOptions{Shallow: p.Shallow, Refresh: true})
			if err != nil {
				return advanceResult{}, err
			}
			target, kind = res.Hash, string(res.Kind)
		}
		if target != before {
			if err := vcs.CheckoutDetached(ctx, p.Path, target); err != nil {
				return advanceResult{}, err
			}
		}
	}
	after, _ := vcs.HeadHash(p.Path)
	out := advanceResult{Ref: ref, Kind: kind, Before: before, Commit: after}
	out.Changed = before == "" || after == "" || before != after
	// updated_at means "when the working tree last moved": only stamp it
	// when something actually changed (or the row was never filled in).
	if out.Changed || ref != p.Ref || kind != p.Kind || after != p.Commit {
		if err := db.SetSnapshot(ctx, p.Name, ref, kind, after, time.Now().UTC()); err != nil {
			return advanceResult{}, err
		}
	}

	// Reindex when HEAD moved, when either hash read failed (reindexing
	// needlessly is safe, serving a stale index is not), or when the
	// checkout was never indexed.
	if out.Changed || p.ParsedAt == nil {
		out.SymbolCount, out.ReindexError = reparseProject(ctx, db, p)
		return out, nil
	}
	if n, err := db.CountSymbols(ctx, p.Name); err == nil {
		out.SymbolCount = n
	}
	return out, nil
}
