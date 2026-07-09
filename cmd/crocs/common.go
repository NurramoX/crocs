package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"crocs/internal/registry"

	"github.com/spf13/cobra"
)

// projectJSON is the shared wire shape for a single project record. Used by
// list (as an element of "projects"), info, fetch (the response carries the
// newly-created project), and remove (echoes what it removed).
type projectJSON struct {
	Name       string  `json:"name"`
	URL        string  `json:"url"`
	Path       string  `json:"path"`
	DefaultRef string  `json:"default_ref,omitempty"`
	Shallow    bool    `json:"shallow"`
	FetchedAt  string  `json:"fetched_at"`
	ParsedAt   *string `json:"parsed_at,omitempty"`
}

func projectToJSON(p registry.Project) projectJSON {
	out := projectJSON{
		Name:       p.Name,
		URL:        p.URL,
		Path:       p.Path,
		DefaultRef: p.DefaultRef,
		Shallow:    p.Shallow,
		FetchedAt:  p.FetchedAt.UTC().Format(time.RFC3339),
	}
	if p.ParsedAt != nil {
		s := p.ParsedAt.UTC().Format(time.RFC3339)
		out.ParsedAt = &s
	}
	return out
}

// cmdCtx returns a context for the command. Falls back to background if
// cobra hasn't wired one (which would only happen in tests without
// SetContext).
func cmdCtx(cmd *cobra.Command) context.Context {
	if c := cmd.Context(); c != nil {
		return c
	}
	return context.Background()
}

// openRegistry opens the registry with a friendlier "the install looks
// foreign" message for the v1→v2 case. Failure here is fatal for callers.
func openRegistry(ctx context.Context) (*registry.DB, error) {
	db, err := registry.Open(ctx)
	if err == nil {
		return db, nil
	}
	if errors.Is(err, registry.ErrIncompatibleSchema) {
		return nil, fmt.Errorf("registry schema mismatch (%w) — this binary expects schema v%d; remove the registry db and re-fetch (PLAN.md §2 Breaking Change #5)", err, registry.SchemaVersion)
	}
	return nil, err
}

// requireProject loads a project by name or returns a clear "not tracked"
// error.
func requireProject(ctx context.Context, db *registry.DB, name string) (registry.Project, error) {
	p, err := db.GetProject(ctx, name)
	if errors.Is(err, registry.ErrNotFound) {
		return registry.Project{}, fmt.Errorf("project %q is not tracked (run `crocs list` to see tracked projects)", name)
	}
	return p, err
}

// shallowHint is the standard hint VCS commands attach on shallow clones;
// missing names what the shallow clone lacks for that command.
func shallowHint(name, missing string) string {
	return fmt.Sprintf("shallow clone: %s; run `crocs unshallow %s` to fetch the rest", missing, name)
}

// capList truncates list to limit entries (0 = no cap) and reports whether
// it did. Shared --limit handling for branches/tags.
func capList[T any](list []T, limit int) ([]T, bool) {
	if limit > 0 && len(list) > limit {
		return list[:limit], true
	}
	return list, false
}
